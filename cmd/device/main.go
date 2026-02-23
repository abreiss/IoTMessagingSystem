package main

import (
	"bufio"
	"fmt"
	"log" //logging
	"os"  // for env variables
	"os/signal"
	"sync"
	"strings"
	"syscall"
	"time" // for sleep

	"github.com/gorilla/websocket" // websockets
)

type consoleUI struct {
	deviceID string
	mu       sync.Mutex
}

func (c *consoleUI) promptLocked() {
	fmt.Fprintf(os.Stdout, "[%s] Enter message (or 'quit' to exit): ", c.deviceID)
}

func (c *consoleUI) Prompt() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.promptLocked()
}

func (c *consoleUI) Println(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Start on a fresh line in case a prompt is currently displayed.
	fmt.Fprint(os.Stdout, "\r\n")
	fmt.Fprintln(os.Stdout, line)
}

func (c *consoleUI) PrintlnAndPrompt(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Start on a fresh line in case a prompt is currently displayed.
	fmt.Fprint(os.Stdout, "\r\n")
	fmt.Fprintln(os.Stdout, line)
	c.promptLocked()
}

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

	ui := &consoleUI{deviceID: deviceID}

	// Goroutine to read from stdin (for interactive testing)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		ui.Prompt()
		for {
			text, err := reader.ReadString('\n')
			if err != nil {
				ui.Println(fmt.Sprintf("[%s] stdin closed (or read error): %v", deviceID, err))
				return
			}
			text = strings.TrimSpace(text)
			if text == "quit" {
				interrupt <- syscall.SIGTERM
				return
			}
			if text == "" {
				ui.Prompt()
				continue
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
				ui.Println(fmt.Sprintf("[%s] Read error: %v", deviceID, err))
				return
			}
			ui.PrintlnAndPrompt(fmt.Sprintf("[%s] Received: %s", deviceID, string(message)))
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
			ui.Println(fmt.Sprintf("[%s] Connection closed", deviceID))
			return
		case <-interrupt:
			ui.Println(fmt.Sprintf("[%s] Shutting down gracefully...", deviceID))
			err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				ui.Println(fmt.Sprintf("[%s] Write close error: %v", deviceID, err))
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		case msg := <-sendCh:
			ui.PrintlnAndPrompt(fmt.Sprintf("[%s] Sent: %s", deviceID, msg))
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
				ui.Println(fmt.Sprintf("[%s] Write error: %v", deviceID, err))
				return
			}
		case <-ticker.C:
		}
	}
}
