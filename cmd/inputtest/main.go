//go:build windows

package main

import (
	"log"
	"time"

	"rdpAiAnswer/internal/input"
)

func main() {
	log.Println("moving mouse to (500,500) in 2s — focus a text field to see the 'a' keypress land")
	time.Sleep(2 * time.Second)

	if err := input.MoveMouse(500, 500); err != nil {
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
