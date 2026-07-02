package main

import (
	"log"
	"net/http"

	"rdpAiAnswer/internal/signaling"
)

func main() {
	reg := signaling.NewRegistry()
	handler := signaling.NewHandler(reg)
	log.Println("rdp-server listening on :9000")
	log.Fatal(http.ListenAndServe(":9000", handler))
}
