package main

import (
	"bufio"           //read stdin
	"encoding/base64" //encode payloads
	"encoding/json"   //encode envelopes
	"fmt"             //formatted output
	"log"             //log device events
	"os"              //env variables
	"os/signal"       //interrupt signals
	"strings"         //string utilities
	"sync"            //mutex
	"syscall"         //signal constants
	"time"            //sleep and ticker

	"github.com/gorilla/websocket" //websockets
)

// Envelope wraps every device-to-device message sent through the relay.
// The relay forwards these as opaque bytes and cannot read the payload.
type Envelope struct {
	Type    string `json:"type"`    // "handshake" or "message"
	From    string `json:"from"`    // sender device ID
	Payload string `json:"payload"` // base64: public key bytes (handshake) or nonce+ciphertext (message)
}

var (
	sharedKey []byte     // AES-256 session key derived after ECDH handshake, nil until complete
	cryptoMu  sync.Mutex // protects sharedKey
)

func main() {
	//get device ID env variable
	deviceID := os.Getenv("DEVICE_ID")
	if deviceID == "" {
		deviceID = "unknown-device"
	}
	//get relay URL env variable
	relayURL := os.Getenv("RELAY_URL")
	if relayURL == "" {
		relayURL = "ws://localhost:8080"
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	log.Printf("Device %s starting with log level: %s", deviceID, logLevel)
	log.Printf("[%s] Connecting to relay at %s", deviceID, relayURL)

	//establish websocket connection to relay
	//conn is connection object, _ is to ignore http
	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	if err != nil {
		log.Fatalf("Failed to connect to relay: %v", err)
	}
	//connection closes when main exits
	defer conn.Close()

	log.Printf("[%s] Connected to relay", deviceID)

	//register with relay by sending device ID as first plain-text message
	err = conn.WriteMessage(websocket.TextMessage, []byte(deviceID))
	if err != nil {
		log.Fatalf("Failed to send device ID: %v", err)
	}

	//generate ephemeral X25519 keypair — private key stays in memory only
	privKey, err := generateKeyPair()
	if err != nil {
		log.Fatalf("Failed to generate keypair: %v", err)
	}
	log.Printf("[%s] Generated ephemeral X25519 keypair", deviceID)

	//connMu serialises writes to conn — main loop and receive goroutine both write
	var connMu sync.Mutex

	//sendHandshake sends our public key to the relay, which forwards it to the other device.
	//called once proactively on startup, and again when we receive a handshake so that
	//a late-joining peer can still complete the exchange.
	sendHandshake := func() {
		pubKey := privKey.PublicKey()
		env := Envelope{
			Type:    "handshake",
			From:    deviceID,
			Payload: encodePubKey(pubKey),
		}
		data, _ := json.Marshal(env)

		connMu.Lock()
		err := conn.WriteMessage(websocket.TextMessage, data)
		connMu.Unlock()
		if err != nil {
			log.Printf("[%s] Failed to send handshake: %v", deviceID, err)
		} else {
			log.Printf("[%s] Sent handshake (public key)", deviceID)
		}
	}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	sendCh := make(chan string, 10)

	//goroutine to read from stdin for interactive testing
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			fmt.Printf("[%s] Enter message (or 'quit' to exit): ", deviceID)
			text, err := reader.ReadString('\n')
			if err != nil {
				log.Printf("[%s] stdin closed (or read error): %v", deviceID, err)
				return
			}
			text = strings.TrimSpace(text)
			if text == "quit" {
				interrupt <- syscall.SIGTERM
				return
			}
			if text != "" {
				sendCh <- text
			}
		}
	}()

	//goroutine to read messages from the relay
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Read error: %v", err)
				return
			}

			//try to parse as a JSON envelope — non-JSON messages are relay system messages (e.g. welcome)
			var env Envelope
			err = json.Unmarshal(raw, &env)
			if err != nil {
				log.Printf("[%s] Relay: %s", deviceID, string(raw))
				continue
			}

			switch env.Type {

			case "handshake":
				//another device sent us their public key
				cryptoMu.Lock()
				alreadyComplete := sharedKey != nil
				cryptoMu.Unlock()

				if alreadyComplete {
					//handshake already done — ignore duplicates (e.g. simultaneous initiation)
					log.Printf("[%s] Ignoring duplicate handshake from %s", deviceID, env.From)
					continue
				}

				//decode peer's raw public key bytes from base64
				peerPubKeyBytes, err := decodePubKey(env.Payload)
				if err != nil {
					log.Printf("[%s] Failed to decode peer public key: %v", deviceID, err)
					continue
				}

				//X25519 ECDH + HKDF-SHA256 → shared AES-256 session key
				//both devices run this with swapped keys and get the same result
				key, err := deriveSharedKey(privKey, peerPubKeyBytes)
				if err != nil {
					log.Printf("[%s] Failed to derive shared key: %v", deviceID, err)
					continue
				}

				cryptoMu.Lock()
				sharedKey = key
				cryptoMu.Unlock()

				log.Printf("[%s] Handshake complete with %s — session key established", deviceID, env.From)

				//respond with our public key so the peer can derive the same key.
				//handles the case where we were online first and the peer never got our initial send.
				sendHandshake()

			case "message":
				//encrypted message from the other device
				cryptoMu.Lock()
				key := sharedKey
				cryptoMu.Unlock()

				if key == nil {
					log.Printf("[%s] Received message before handshake complete, ignoring", deviceID)
					continue
				}

				//decode base64 payload (nonce + ciphertext + GCM tag)
				cipherBytes, err := base64.StdEncoding.DecodeString(env.Payload)
				if err != nil {
					log.Printf("[%s] Failed to decode message payload: %v", deviceID, err)
					continue
				}

				//decrypt and authenticate with AES-256-GCM
				plaintext, err := decryptMessage(key, cipherBytes)
				if err != nil {
					log.Printf("[%s] Failed to decrypt message from %s: %v", deviceID, env.From, err)
					continue
				}

				log.Printf("[%s] Received from %s: %s", deviceID, env.From, string(plaintext))
			}
		}
	}()

	//send initial handshake after a short delay to let the relay welcome message arrive first
	time.Sleep(1 * time.Second)
	sendHandshake()

	//main event loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			log.Println("Connection closed")
			return

		case <-interrupt:
			log.Println("Shutting down gracefully...")
			closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
			connMu.Lock()
			err := conn.WriteMessage(websocket.CloseMessage, closeMsg)
			connMu.Unlock()
			if err != nil {
				log.Printf("Write close error: %v", err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return

		case msg := <-sendCh:
			//handshake must complete before sending encrypted messages
			cryptoMu.Lock()
			key := sharedKey
			cryptoMu.Unlock()

			if key == nil {
				log.Printf("[%s] Handshake not yet complete — cannot send message", deviceID)
				continue
			}

			//encrypt plaintext with AES-256-GCM using the shared session key
			plaintextBytes := []byte(msg)
			cipherBytes, err := encryptMessage(key, plaintextBytes)
			if err != nil {
				log.Printf("[%s] Encryption failed: %v", deviceID, err)
				continue
			}

			//wrap encrypted payload in an envelope for the relay to forward
			env := Envelope{
				Type:    "message",
				From:    deviceID,
				Payload: base64.StdEncoding.EncodeToString(cipherBytes),
			}
			data, _ := json.Marshal(env)

			connMu.Lock()
			err = conn.WriteMessage(websocket.TextMessage, data)
			connMu.Unlock()
			if err != nil {
				log.Printf("[%s] Write error: %v", deviceID, err)
				return
			}
			log.Printf("[%s] Sent encrypted message", deviceID)

		case <-ticker.C:
			//heartbeat tick (no-op)
		}
	}
}
