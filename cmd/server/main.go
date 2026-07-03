package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"rdpAiAnswer/internal/signaling"
	"rdpAiAnswer/internal/webui"
)

// Compiled-in defaults, same mechanism as cmd/host — override at build
// time with -ldflags (`make server ADDR=... ICE_SERVERS=...`), or at
// runtime with the matching flag.
var (
	defaultAddr           = ":9000"
	defaultICEServers     = ""
	defaultTURNUsername   = ""
	defaultTURNCredential = ""
)

func main() {
	addr := flag.String("addr", defaultAddr, "address to listen on")
	iceServers := flag.String("ice-servers", defaultICEServers, "comma-separated STUN/TURN URLs served to the admin UI, e.g. stun:vps:3478,turn:vps:3478")
	turnUsername := flag.String("turn-username", defaultTURNUsername, "TURN username, if any TURN URL is passed in -ice-servers")
	turnCredential := flag.String("turn-credential", defaultTURNCredential, "TURN credential, if any TURN URL is passed in -ice-servers")
	flag.Parse()

	var urls []string
	if *iceServers != "" {
		urls = strings.Split(*iceServers, ",")
	}

	reg := signaling.NewRegistry()
	sigHandler := signaling.NewHandler(reg)

	mux := http.NewServeMux()
	mux.Handle("/ws/", sigHandler)
	mux.HandleFunc("/config", webui.ConfigHandler(urls, *turnUsername, *turnCredential))
	mux.Handle("/", webui.Handler())

	log.Printf("rdp-server listening on %s (admin UI at http://localhost%s/)", *addr, *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
