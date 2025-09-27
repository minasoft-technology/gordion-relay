package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"log"
	"time"

	"github.com/quic-go/quic-go"
)

func main() {
	tlsConf := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"tunnel-v1"},
	}

	conn, err := quic.DialAddr(context.Background(), "localhost:443", tlsConf, &quic.Config{
		MaxIdleTimeout: 30 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.CloseWithError(0, "test complete")

	log.Println("Connected to relay server")

	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		log.Fatalf("Failed to open stream: %v", err)
	}
	defer stream.Close()

	registrationMsg := "REGISTER ankara ankara.localhost test_token_ankara_123\n"
	log.Printf("Sending registration: %s", registrationMsg)

	if _, err := stream.Write([]byte(registrationMsg)); err != nil {
		log.Fatalf("Failed to send registration: %v", err)
	}

	reader := bufio.NewReader(stream)
	response, err := reader.ReadString('\n')
	if err != nil {
		log.Fatalf("Failed to read response: %v", err)
	}

	log.Printf("Registration response: %s", response)

	if response == "OK Registered\n" {
		log.Println("✓ Successfully registered!")

		time.Sleep(2 * time.Second)

		heartbeatStream, err := conn.OpenStreamSync(context.Background())
		if err != nil {
			log.Fatalf("Failed to open heartbeat stream: %v", err)
		}
		defer heartbeatStream.Close()

		if _, err := heartbeatStream.Write([]byte("HEARTBEAT\n")); err != nil {
			log.Printf("Failed to send heartbeat: %v", err)
		} else {
			log.Println("✓ Heartbeat sent")
		}

		time.Sleep(2 * time.Second)
		log.Println("Test complete!")
	} else {
		log.Fatalf("Registration failed: %s", response)
	}
}