//go:build windows || linux

package main

import (
	"log"
	"time"

	"rdpAiAnswer/internal/capture"
	"rdpAiAnswer/internal/input"
)

func main() {
	_, screenW, screenH, err := capture.GrabPrimaryJPEG(1)
	if err != nil {
		log.Fatal("couldn't determine screen dimensions: ", err)
	}

	log.Printf("moving mouse to (500,500) of %dx%d in 2s — focus a text field to see the 'a' keypress land", screenW, screenH)
	time.Sleep(2 * time.Second)

	if err := input.MoveMouse(500, 500, screenW, screenH); err != nil {
		log.Fatal(err)
	}

	if err := input.KeyPress(0x41 /* VK_A */, true); err != nil {
		log.Fatal(err)
	}
	if err := input.KeyPress(0x41, false); err != nil {
		log.Fatal(err)
	}

	log.Println("done")
}
