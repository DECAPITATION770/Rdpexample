// Package webui embeds and serves the single-page admin UI (session list
// + WebRTC control window) that browsers load directly from the
// signaling server — no separate Go GUI client needed.
package webui

import (
	_ "embed"
	"net/http"
)

//go:embed admin.html
var adminHTML []byte

// Handler serves the embedded admin page at any path it's mounted on.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(adminHTML)
	}
}
