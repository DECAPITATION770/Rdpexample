// internal/signaling/handler.go
package signaling

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"rdpAiAnswer/internal/proto"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler routes host and viewer WebSocket connections. Host connections
// register themselves and stay open (registration lives as long as the
// socket does). Viewer connections request the session list and relay
// SDP/ICE messages to a specific host by session ID.
type Handler struct {
	reg   *Registry
	mux   *http.ServeMux
	hosts map[string]*websocket.Conn // sessionID -> host socket, for relay
}

func NewHandler(reg *Registry) *Handler {
	h := &Handler{reg: reg, mux: http.NewServeMux(), hosts: make(map[string]*websocket.Conn)}
	h.mux.HandleFunc("/ws/host", h.handleHost)
	h.mux.HandleFunc("/ws/viewer", h.handleViewer)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleHost(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var sessionID string
	for {
		var env proto.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			if sessionID != "" {
				h.reg.Unregister(sessionID)
				delete(h.hosts, sessionID)
			}
			return
		}
		switch env.Type {
		case proto.MsgRegisterHost:
			var reg proto.RegisterHost
			if err := json.Unmarshal(env.Payload, &reg); err != nil {
				continue
			}
			sessionID = h.reg.Register(reg.Name)
			h.hosts[sessionID] = conn
		case proto.MsgAnswer, proto.MsgICECandidate:
			// relayed on to the viewer in a later task once we track
			// per-viewer connections; logged for now.
			log.Printf("host %s sent %s (relay wiring added in Task 7/webrtcconn integration)", sessionID, env.Type)
		}
	}
}

func (h *Handler) handleViewer(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	for {
		var env proto.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return
		}
		switch env.Type {
		case proto.MsgListSessions:
			list := proto.SessionList{Sessions: h.reg.List()}
			payload, _ := json.Marshal(list)
			_ = conn.WriteJSON(proto.Envelope{Type: proto.MsgSessionList, Payload: payload})
		case proto.MsgOffer, proto.MsgICECandidate:
			log.Printf("viewer sent %s for session %s (relay wiring added in Task 7)", env.Type, env.SessionID)
		}
	}
}
