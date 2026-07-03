//go:build windows

package main

import (
	"log"
	"os"

	"rdpAiAnswer/internal/capture"
)

func main() {
	data, err := capture.GrabPrimaryJPEG(80)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("capture_test.jpg", data, 0644); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote capture_test.jpg —", len(data), "bytes")
}
