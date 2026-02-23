package main

import (
	"bufio"
	"fmt"
	"log" //logging
	"os"  // for env variables
	"os/signal"
	"strings"
	"syscall"
	"time" // for sleep

	"github.com/gorilla/websocket" // websockets
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
	//connection close when main exits
	defer conn.Close()
	//log successful connection
	log.Printf("[%s] Connected to relay", deviceID)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(deviceID)); err != nil {
		log.Fatalf("Failed to send device ID: %v", err)
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	sendCh := make(chan string, 10)

	// Goroutine to read from stdin (for interactive testing)
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

	// Goroutine to read messages from relay
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Read error: %v", err)
				return
			}
			log.Printf("Received: %s", string(message))
		}
	}()

	// Send a test message after connection
	time.Sleep(1 * time.Second)
	//testMsg := fmt.Sprintf("Hello from %s!", deviceID)
	//log.Printf("Sending test message: %s", testMsg)
	//if err := conn.WriteMessage(websocket.TextMessage, []byte(testMsg)); err != nil {
	//	log.Printf("Failed to send test message: %v", err)
	//}

	// Main loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			log.Println("Connection closed")
			return
		case <-interrupt:
			log.Println("Shutting down gracefully...")
			err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Printf("Write close error: %v", err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		case msg := <-sendCh:
			log.Printf("Sending: %s", msg)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
				log.Printf("Write error: %v", err)
				return
			}
		case <-ticker.C:
		}
	}
}
