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

func TestHandler_RelaysOfferAnswerAndICEBetweenViewerAndHost(t *testing.T) {
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
	var listResp proto.Envelope
	if err := viewerConn.ReadJSON(&listResp); err != nil {
		t.Fatalf("read session list: %v", err)
	}
	var list proto.SessionList
	if err := json.Unmarshal(listResp.Payload, &list); err != nil {
		t.Fatalf("unmarshal session list: %v", err)
	}
	sessionID := list.Sessions[0].SessionID

	// viewer -> host: offer
	offerPayload, _ := json.Marshal(proto.SDPMessage{SDP: "offer-sdp"})
	if err := viewerConn.WriteJSON(proto.Envelope{Type: proto.MsgOffer, SessionID: sessionID, Payload: offerPayload}); err != nil {
		t.Fatalf("write offer: %v", err)
	}

	hostConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var gotOffer proto.Envelope
	if err := hostConn.ReadJSON(&gotOffer); err != nil {
		t.Fatalf("host read offer: %v", err)
	}
	if gotOffer.Type != proto.MsgOffer {
		t.Fatalf("gotOffer.Type = %v, want offer", gotOffer.Type)
	}
	var gotOfferSDP proto.SDPMessage
	_ = json.Unmarshal(gotOffer.Payload, &gotOfferSDP)
	if gotOfferSDP.SDP != "offer-sdp" {
		t.Fatalf("gotOfferSDP.SDP = %q, want %q", gotOfferSDP.SDP, "offer-sdp")
	}

	// host -> viewer: answer
	answerPayload, _ := json.Marshal(proto.SDPMessage{SDP: "answer-sdp"})
	if err := hostConn.WriteJSON(proto.Envelope{Type: proto.MsgAnswer, SessionID: sessionID, Payload: answerPayload}); err != nil {
		t.Fatalf("write answer: %v", err)
	}

	viewerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var gotAnswer proto.Envelope
	if err := viewerConn.ReadJSON(&gotAnswer); err != nil {
		t.Fatalf("viewer read answer: %v", err)
	}
	if gotAnswer.Type != proto.MsgAnswer {
		t.Fatalf("gotAnswer.Type = %v, want answer", gotAnswer.Type)
	}
	var gotAnswerSDP proto.SDPMessage
	_ = json.Unmarshal(gotAnswer.Payload, &gotAnswerSDP)
	if gotAnswerSDP.SDP != "answer-sdp" {
		t.Fatalf("gotAnswerSDP.SDP = %q, want %q", gotAnswerSDP.SDP, "answer-sdp")
	}

	// viewer -> host: ICE candidate
	icePayload, _ := json.Marshal(proto.ICECandidateMessage{Candidate: "cand-from-viewer"})
	if err := viewerConn.WriteJSON(proto.Envelope{Type: proto.MsgICECandidate, SessionID: sessionID, Payload: icePayload}); err != nil {
		t.Fatalf("write viewer ICE: %v", err)
	}
	var gotViewerICE proto.Envelope
	if err := hostConn.ReadJSON(&gotViewerICE); err != nil {
		t.Fatalf("host read ICE: %v", err)
	}
	var gotViewerICEMsg proto.ICECandidateMessage
	_ = json.Unmarshal(gotViewerICE.Payload, &gotViewerICEMsg)
	if gotViewerICEMsg.Candidate != "cand-from-viewer" {
		t.Fatalf("gotViewerICEMsg.Candidate = %q, want %q", gotViewerICEMsg.Candidate, "cand-from-viewer")
	}

	// host -> viewer: ICE candidate
	hostIcePayload, _ := json.Marshal(proto.ICECandidateMessage{Candidate: "cand-from-host"})
	if err := hostConn.WriteJSON(proto.Envelope{Type: proto.MsgICECandidate, SessionID: sessionID, Payload: hostIcePayload}); err != nil {
		t.Fatalf("write host ICE: %v", err)
	}
	var gotHostICE proto.Envelope
	if err := viewerConn.ReadJSON(&gotHostICE); err != nil {
		t.Fatalf("viewer read ICE: %v", err)
	}
	var gotHostICEMsg proto.ICECandidateMessage
	_ = json.Unmarshal(gotHostICE.Payload, &gotHostICEMsg)
	if gotHostICEMsg.Candidate != "cand-from-host" {
		t.Fatalf("gotHostICEMsg.Candidate = %q, want %q", gotHostICEMsg.Candidate, "cand-from-host")
	}
}
