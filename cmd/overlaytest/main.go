//go:build windows

package main

import (
	"log"
	"time"

	"rdpAiAnswer/internal/overlay"
)

func main() {
	log.Println("showing overlay message in 2s — move your cursor where you want to see it")
	time.Sleep(2 * time.Second)

	if err := overlay.ShowMessage("привет", 2*time.Second, "#ffcc00"); err != nil {
		log.Fatal(err)
	}

	log.Println("done")
}
