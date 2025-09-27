package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	// WebSocket dialer with TLS config
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // For testing with self-signed certs
		},
	}

	// Connect to relay server
	url := "wss://localhost/tunnel"
	log.Printf("Connecting to %s", url)

	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	log.Println("✓ Connected to relay server")

	// Send registration message
	registrationMsg := "REGISTER ankara ankara.localhost test_token_ankara_123"
	log.Printf("Sending registration: %s", registrationMsg)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(registrationMsg)); err != nil {
		log.Fatalf("Failed to send registration: %v", err)
	}

	// Read response
	_, response, err := conn.ReadMessage()
	if err != nil {
		log.Fatalf("Failed to read response: %v", err)
	}

	log.Printf("Registration response: %s", string(response))

	if string(response) == "OK Registered" {
		log.Println("✓ Successfully registered!")

		// Send heartbeat
		time.Sleep(2 * time.Second)
		log.Println("Sending heartbeat...")

		if err := conn.WriteMessage(websocket.TextMessage, []byte("HEARTBEAT")); err != nil {
			log.Printf("Failed to send heartbeat: %v", err)
		} else {
			log.Println("✓ Heartbeat sent")
		}

		// Keep connection alive
		log.Println("Keeping connection alive for 10 seconds...")
		time.Sleep(10 * time.Second)

		log.Println("✓ Test complete!")
	} else {
		log.Fatalf("Registration failed: %s", string(response))
	}
}