// internal/signaling/handler.go
package signaling

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"rdpAiAnswer/internal/proto"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler routes host and viewer WebSocket connections. Host connections
// register themselves and stay open (registration lives as long as the
// socket does). Viewer connections request the session list and relay
// SDP/ICE messages to a specific host by session ID; the handler records
// which viewer socket is talking to which session (from the SessionID on
// its first offer/ICE message) so the host's replies can be relayed back
// to the right viewer. Only one active viewer per session is tracked at a
// time, matching the MVP's one-admin-at-a-time design.
type Handler struct {
	mu      sync.Mutex
	reg     *Registry
	mux     *http.ServeMux
	hosts   map[string]*websocket.Conn // sessionID -> host socket
	viewers map[string]*websocket.Conn // sessionID -> viewer socket
}

func NewHandler(reg *Registry) *Handler {
	h := &Handler{
		reg:     reg,
		mux:     http.NewServeMux(),
		hosts:   make(map[string]*websocket.Conn),
		viewers: make(map[string]*websocket.Conn),
	}
	h.mux.HandleFunc("/ws/host", h.handleHost)
	h.mux.HandleFunc("/ws/viewer", h.handleViewer)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) setHost(sessionID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hosts[sessionID] = conn
}

func (h *Handler) removeHost(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.hosts, sessionID)
}

func (h *Handler) setViewer(sessionID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.viewers[sessionID] = conn
}

func (h *Handler) hostConn(sessionID string) *websocket.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hosts[sessionID]
}

func (h *Handler) viewerConn(sessionID string) *websocket.Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.viewers[sessionID]
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
				h.removeHost(sessionID)
				log.Printf("signaling: host session %s disconnected (%v)", sessionID, err)
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
			h.setHost(sessionID, conn)
			log.Printf("signaling: host %q registered as session %s", reg.Name, sessionID)
		case proto.MsgAnswer, proto.MsgICECandidate:
			env.SessionID = sessionID
			if viewer := h.viewerConn(sessionID); viewer != nil {
				_ = viewer.WriteJSON(env)
				log.Printf("signaling: relayed %s from host %s to viewer", env.Type, sessionID)
			} else {
				log.Printf("signaling: host %s sent %s but no viewer is attached to this session", sessionID, env.Type)
			}
		}
	}
}

func (h *Handler) handleViewer(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	log.Printf("signaling: viewer connected from %s", r.RemoteAddr)

	for {
		var env proto.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			log.Printf("signaling: viewer %s disconnected (%v)", r.RemoteAddr, err)
			return
		}
		switch env.Type {
		case proto.MsgListSessions:
			list := proto.SessionList{Sessions: h.reg.List()}
			payload, _ := json.Marshal(list)
			_ = conn.WriteJSON(proto.Envelope{Type: proto.MsgSessionList, Payload: payload})
			log.Printf("signaling: sent session list (%d sessions) to viewer %s", len(list.Sessions), r.RemoteAddr)
		case proto.MsgOffer, proto.MsgICECandidate:
			h.setViewer(env.SessionID, conn)
			if host := h.hostConn(env.SessionID); host != nil {
				_ = host.WriteJSON(env)
				log.Printf("signaling: relayed %s from viewer to host session %s", env.Type, env.SessionID)
			} else {
				log.Printf("signaling: viewer sent %s for session %s but no host with that ID is connected (stale session list?)", env.Type, env.SessionID)
			}
		}
	}
}
