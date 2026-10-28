package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"time"

	"onespace.local/hub/coordination"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	coordinator := flag.String("coordinator", "", "HTTPS coordinator base URL")
	token := flag.String("token", "", "device token; provide through a protected service environment")
	device := flag.String("device", "", "enrolled Pi device ID")
	upstream := flag.String("upstream", "http://127.0.0.1:8889/camera/whep", "fixed loopback MediaMTX WHEP URL")
	flag.Parse()
	if *coordinator == "" || *token == "" || *device == "" {
		return errors.New("coordinator, token and device are required")
	}
	client := coordination.Client{BaseURL: *coordinator, Token: *token, HTTP: &http.Client{Timeout: 65 * time.Second}}
	bridge := coordination.WHEPBridge{Coordinator: client, Device: *device, Upstream: *upstream}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		requests, err := client.Requests(ctx)
		cancel()
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		for _, request := range requests {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if err := bridge.ServeRequest(ctx, request); err != nil {
				log.Printf("camera signaling session %s failed: %v", request.Session, err)
				_ = client.Close(context.Background(), request.Session, *device)
			}
			cancel()
		}
		time.Sleep(500 * time.Millisecond)
	}
}
