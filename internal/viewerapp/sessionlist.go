package viewerapp

import (
	"log"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"rdpAiAnswer/internal/proto"
)

// ShowSessionList opens the main window: a refreshable list of online
// hosts fetched from the signaling server, opening a control window on
// click.
func ShowSessionList(fyneApp fyne.App, signalingBaseURL string) {
	client, err := newSignalingClient(signalingBaseURL)
	if err != nil {
		log.Printf("viewerapp: failed to connect to signaling server: %v", err)
	}

	var sessions []proto.SessionInfo

	w := fyneApp.NewWindow("RDP-Tool — Sessions")

	list := widget.NewList(
		func() int { return len(sessions) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(sessions[i].Name)
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		if i < 0 || i >= len(sessions) {
			return
		}
		OpenControlWindow(fyneApp, client, sessions[i])
		list.UnselectAll()
	}

	refresh := widget.NewButton("Refresh", func() {
		if client == nil {
			c, err := newSignalingClient(signalingBaseURL)
			if err != nil {
				log.Printf("viewerapp: failed to connect to signaling server: %v", err)
				return
			}
			client = c
		}
		got, err := client.requestSessionList(3 * time.Second)
		if err != nil {
			log.Printf("viewerapp: fetch sessions failed: %v", err)
			return
		}
		sessions = got
		list.Refresh()
	})

	w.SetContent(container.NewBorder(nil, refresh, nil, nil, list))
	w.Resize(fyne.NewSize(400, 300))
	w.Show()
}
