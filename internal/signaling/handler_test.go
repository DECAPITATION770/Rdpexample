// internal/signaling/handler_test.go
package signaling

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"rdpAiAnswer/internal/proto"
)

func TestHandler_HostRegisters_ViewerSeesSession(t *testing.T) {
	reg := NewRegistry()
	srv := httptest.NewServer(NewHandler(reg))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	hostConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/host", nil)
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer hostConn.Close()

	payload, _ := json.Marshal(proto.RegisterHost{Name: "PC-OFFICE-1"})
	if err := hostConn.WriteJSON(proto.Envelope{Type: proto.MsgRegisterHost, Payload: payload}); err != nil {
		t.Fatalf("write register: %v", err)
	}

	// give server a moment to process registration
	time.Sleep(50 * time.Millisecond)

	viewerConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/viewer", nil)
	if err != nil {
		t.Fatalf("dial viewer: %v", err)
	}
	defer viewerConn.Close()

	if err := viewerConn.WriteJSON(proto.Envelope{Type: proto.MsgListSessions}); err != nil {
		t.Fatalf("write list request: %v", err)
	}

	viewerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var resp proto.Envelope
	if err := viewerConn.ReadJSON(&resp); err != nil {
		t.Fatalf("read session list: %v", err)
	}
	if resp.Type != proto.MsgSessionList {
		t.Fatalf("resp.Type = %v, want session_list", resp.Type)
	}

	var list proto.SessionList
	if err := json.Unmarshal(resp.Payload, &list); err != nil {
		t.Fatalf("unmarshal session list: %v", err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].Name != "PC-OFFICE-1" {
		t.Fatalf("unexpected session list: %+v", list.Sessions)
	}
}
