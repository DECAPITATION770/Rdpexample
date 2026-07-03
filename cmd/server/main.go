package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"rdpAiAnswer/internal/signaling"
	"rdpAiAnswer/internal/webui"
)

func main() {
	addr := flag.String("addr", ":9000", "address to listen on")
	iceServers := flag.String("ice-servers", "", "comma-separated STUN/TURN URLs served to the admin UI, e.g. stun:vps:3478,turn:vps:3478")
	turnUsername := flag.String("turn-username", "", "TURN username, if any TURN URL is passed in -ice-servers")
	turnCredential := flag.String("turn-credential", "", "TURN credential, if any TURN URL is passed in -ice-servers")
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
