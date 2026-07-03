package viewerapp

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"rdpAiAnswer/internal/proto"
)

// OpenControlWindow is a placeholder for Task 12 so the session list
// screen compiles and is independently testable; Task 13 replaces this
// with the full video + input-toggle + hotkey-rebind + overlay-message
// control window.
func OpenControlWindow(fyneApp fyne.App, client *signalingClient, session proto.SessionInfo) {
	w := fyneApp.NewWindow(session.Name)
	w.SetContent(widget.NewLabel("Connecting to " + session.Name + "... (full control window lands in Task 13)"))
	w.Resize(fyne.NewSize(300, 100))
	w.Show()
}
