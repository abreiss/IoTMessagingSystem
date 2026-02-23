package main

import (
	"fmt"
	"log"      //log server events
	"net/http" //http libary
	"os"
	"sync"

	"github.com/gorilla/websocket" //websockets
)

// prevent cross-origin issues with websockets
// allowing all conections
var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for testing
		},
	}
	clients   = make(map[*websocket.Conn]string) // conn -> device ID
	clientsMu sync.RWMutex
)

func main() {
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	log.Printf("Starting relay server with log level: %s", logLevel)

	http.HandleFunc("/", handleWebSocket)

	port := ":8080"
	log.Printf("Relay server listening on %s", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal("ListenAndServe error:", err)
	}
}

// manage websocket connections and messages
// w is the response writer, r is the request object
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	//upgrade the HTTP connection to a websocket connection
	//conn is connection object
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Upgrade error: %v", err)
		return
	}
	//close when function exits
	defer conn.Close()

	// Read device ID from first message
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Printf("Read error: %v", err)
		return
	}

	deviceID := string(msg)
	log.Printf("Device connected: %s from %s", deviceID, conn.RemoteAddr())

	clientsMu.Lock()
	clients[conn] = deviceID
	clientCount := len(clients)
	clientsMu.Unlock()

	log.Printf("Active clients: %d", clientCount)

	// Send welcome message
	welcomeMsg := fmt.Sprintf("Welcome %s! You are connected to the relay server.", deviceID)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(welcomeMsg)); err != nil {
		log.Printf("Write error: %v", err)
		return
	}

	// Handle incoming messages
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Device %s disconnected: %v", deviceID, err)
			break
		}

		log.Printf("Received from %s: %s", deviceID, string(message))

		// Relay message to all other clients
		clientsMu.RLock()
		for client, id := range clients {
			if client != conn {
				if err := client.WriteMessage(messageType, message); err != nil {
					log.Printf("Error relaying to %s: %v", id, err)
				} else {
					log.Printf("Relayed message from %s to %s", deviceID, id)
				}
			}
		}
		clientsMu.RUnlock()
	}

	// Clean up
	clientsMu.Lock()
	delete(clients, conn)
	clientsMu.Unlock()
	log.Printf("Device %s removed from active clients", deviceID)
}
