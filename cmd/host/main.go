//go:build windows

package main

import (
	"flag"
	"log"
	"strings"

	"rdpAiAnswer/internal/hostapp"
)

func main() {
	name := flag.String("name", "unnamed-host", "display name shown in the viewer's session list")
	server := flag.String("server", "ws://localhost:9000/ws/host", "signaling server URL")
	iceServers := flag.String("ice-servers", "", "comma-separated STUN/TURN URLs, e.g. stun:vps:3478,turn:vps:3478")
	flag.Parse()

	cfg := hostapp.Config{SignalingURL: *server, Name: *name}
	if *iceServers != "" {
		cfg.ICEServers = strings.Split(*iceServers, ",")
	}

	if err := hostapp.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
