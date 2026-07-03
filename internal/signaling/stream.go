// internal/signaling/stream.go
package signaling

import (
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"

	"rdpAiAnswer/internal/proto"
)

func (h *Handler) sendToHost(sessionID string, env proto.Envelope) {
	if host := h.hostConn(sessionID); host != nil {
		_ = host.WriteJSON(env)
	}
}

// handleStream serves GET /stream/{session_id} as a multipart/x-mixed-replace
// MJPEG feed — the plain-HTTP fallback for when WebRTC can't connect at
// all. It asks the host to start pushing frames on the first subscriber
// and to stop on the last, so an idle host with nobody watching over
// HTTP doesn't pay any relay cost.
func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if h.hostConn(sessionID) == nil {
		http.Error(w, "session not connected", http.StatusServiceUnavailable)
		return
	}

	bc := h.broadcaster(sessionID)
	ch, cancel, isFirst := bc.SubscribeTracked()
	if isFirst {
		h.sendToHost(sessionID, proto.Envelope{Type: proto.MsgStartFrameRelay, SessionID: sessionID})
	}
	defer func() {
		if isLast := cancel(); isLast {
			h.sendToHost(sessionID, proto.Envelope{Type: proto.MsgStopFrameRelay, SessionID: sessionID})
		}
	}()

	const boundary = "frame"
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	mw := multipart.NewWriter(w)
	_ = mw.SetBoundary(boundary)

	for {
		select {
		case jpeg := <-ch:
			part, err := mw.CreatePart(textproto.MIMEHeader{
				"Content-Type":   {"image/jpeg"},
				"Content-Length": {strconv.Itoa(len(jpeg))},
			})
			if err != nil {
				return
			}
			if _, err := part.Write(jpeg); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}
