package main

import (
	"flag"

	"fyne.io/fyne/v2/app"

	"rdpAiAnswer/internal/viewerapp"
)

func main() {
	server := flag.String("server", "ws://localhost:9000", "signaling server base URL")
	flag.Parse()

	a := app.New()
	viewerapp.ShowSessionList(a, *server)
	a.Run()
}
