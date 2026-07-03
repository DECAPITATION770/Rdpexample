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
	turnUsername := flag.String("turn-username", "", "TURN username, if any TURN URL is passed in -ice-servers")
	turnCredential := flag.String("turn-credential", "", "TURN credential, if any TURN URL is passed in -ice-servers")
	flag.Parse()

	cfg := hostapp.Config{SignalingURL: *server, Name: *name, ICEUsername: *turnUsername, ICECredential: *turnCredential}
	if *iceServers != "" {
		cfg.ICEServers = strings.Split(*iceServers, ",")
	}

	if err := hostapp.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
