//go:build windows

package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"rdpAiAnswer/internal/hostapp"
)

// These are the compiled-in defaults. Override them at build time with
// -ldflags, e.g.:
//
//	go build -ldflags "-X main.defaultServer=wss://mydomain.com/ws/host -X main.defaultICEServers=stun:mydomain.com:3478,turn:mydomain.com:3478 -X main.defaultTURNUsername=rdp -X main.defaultTURNCredential=secret" -o rdp-host.exe ./cmd/host
//
// (the Makefile's `host` target does this for you — see `make host SERVER=... ICE_SERVERS=...`).
// The resulting .exe then runs with zero flags needed on the target PC;
// passing a flag at runtime still overrides the baked-in value.
var (
	defaultServer         = "ws://localhost:9000/ws/host"
	defaultICEServers     = ""
	defaultTURNUsername   = ""
	defaultTURNCredential = ""
)

func main() {
	hostname, _ := os.Hostname()

	name := flag.String("name", hostname, "display name shown in the admin's session list (defaults to this PC's hostname)")
	server := flag.String("server", defaultServer, "signaling server URL")
	iceServers := flag.String("ice-servers", defaultICEServers, "comma-separated STUN/TURN URLs, e.g. stun:vps:3478,turn:vps:3478")
	turnUsername := flag.String("turn-username", defaultTURNUsername, "TURN username, if any TURN URL is passed in -ice-servers")
	turnCredential := flag.String("turn-credential", defaultTURNCredential, "TURN credential, if any TURN URL is passed in -ice-servers")
	flag.Parse()

	cfg := hostapp.Config{SignalingURL: *server, Name: *name, ICEUsername: *turnUsername, ICECredential: *turnCredential}
	if *iceServers != "" {
		cfg.ICEServers = strings.Split(*iceServers, ",")
	}

	log.Printf("connecting to %s as %q", cfg.SignalingURL, cfg.Name)
	if err := hostapp.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
