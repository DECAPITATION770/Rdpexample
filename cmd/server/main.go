package main

import (
	"flag"
	"log"
	"net/http"

	"rdpAiAnswer/internal/signaling"
)

func main() {
	addr := flag.String("addr", ":9000", "address to listen on")
	flag.Parse()

	reg := signaling.NewRegistry()
	handler := signaling.NewHandler(reg)
	log.Printf("rdp-server listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
