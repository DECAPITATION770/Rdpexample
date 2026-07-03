package signaling

import (
	"context"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"rdpAiAnswer/internal/proto"
)

func TestHandler_StreamEndpoint_DeliversFramesAndSignalsHostStartStop(t *testing.T) {
	reg := NewRegistry()
	srv := httptest.NewServer(NewHandler(reg))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	hostConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/host", nil)
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer hostConn.Close()

	payload, _ := json.Marshal(proto.RegisterHost{Name: "PC-1"})
	hostConn.WriteJSON(proto.Envelope{Type: proto.MsgRegisterHost, Payload: payload})

	viewerConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/viewer", nil)
	if err != nil {
		t.Fatalf("dial viewer: %v", err)
	}
	defer viewerConn.Close()
	viewerConn.WriteJSON(proto.Envelope{Type: proto.MsgListSessions})
	viewerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var listResp proto.Envelope
	viewerConn.ReadJSON(&listResp)
	var list proto.SessionList
	json.Unmarshal(listResp.Payload, &list)
	sessionID := list.Sessions[0].SessionID

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream/"+sessionID, nil)
	client := &http.Client{}
	respCh := make(chan *http.Response, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		respCh <- resp
	}()

	// The GET should trigger a start_frame_relay to the host.
	hostConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var startEnv proto.Envelope
	if err := hostConn.ReadJSON(&startEnv); err != nil {
		t.Fatalf("host read start_frame_relay: %v", err)
	}
	if startEnv.Type != proto.MsgStartFrameRelay {
		t.Fatalf("startEnv.Type = %v, want start_frame_relay", startEnv.Type)
	}

	resp := <-respCh
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/x-mixed-replace") {
		t.Fatalf("Content-Type = %q, want multipart/x-mixed-replace prefix", ct)
	}
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}

	// Host pushes one frame.
	framePayload, _ := json.Marshal(proto.RelayFrameMessage{JPEG: []byte("fake-jpeg-bytes")})
	if err := hostConn.WriteJSON(proto.Envelope{Type: proto.MsgRelayFrame, SessionID: sessionID, Payload: framePayload}); err != nil {
		t.Fatalf("write relay frame: %v", err)
	}

	mr := multipart.NewReader(resp.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart: %v", err)
	}
	buf := make([]byte, len("fake-jpeg-bytes"))
	if _, err := part.Read(buf); err != nil {
		t.Fatalf("read part: %v", err)
	}
	if string(buf) != "fake-jpeg-bytes" {
		t.Fatalf("part body = %q, want %q", buf, "fake-jpeg-bytes")
	}

	// Client disconnect should trigger stop_frame_relay.
	cancel()
	hostConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var stopEnv proto.Envelope
	if err := hostConn.ReadJSON(&stopEnv); err != nil {
		t.Fatalf("host read stop_frame_relay: %v", err)
	}
	if stopEnv.Type != proto.MsgStopFrameRelay {
		t.Fatalf("stopEnv.Type = %v, want stop_frame_relay", stopEnv.Type)
	}
}
