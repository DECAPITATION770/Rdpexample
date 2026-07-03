package main

import (
	"flag"
	"log"
	"net/http"

	"rdpAiAnswer/internal/signaling"
	"rdpAiAnswer/internal/webui"
)

func main() {
	addr := flag.String("addr", ":9000", "address to listen on")
	flag.Parse()

	reg := signaling.NewRegistry()
	sigHandler := signaling.NewHandler(reg)

	mux := http.NewServeMux()
	mux.Handle("/ws/", sigHandler)
	mux.Handle("/", webui.Handler())

	log.Printf("rdp-server listening on %s (admin UI at http://localhost%s/)", *addr, *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
