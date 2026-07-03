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

// safeConn wraps a websocket.Conn with a write mutex. gorilla/websocket
// allows only one concurrent writer per connection; relaying (e.g. a
// host's answer/screenshot forwarded to a viewer) writes to a
// connection from a different goroutine than the one that owns its read
// loop, so every write needs to go through this lock. Reads are never
// concurrent (only the owning handleHost/handleViewer goroutine reads),
// so ReadJSON needs no locking.
type safeConn struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func newSafeConn(conn *websocket.Conn) *safeConn { return &safeConn{conn: conn} }

func (c *safeConn) WriteJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

func (c *safeConn) ReadJSON(v any) error { return c.conn.ReadJSON(v) }
func (c *safeConn) Close() error         { return c.conn.Close() }

// ReadMessage returns the raw WebSocket message type and payload. Only
// handleHost uses this directly (instead of ReadJSON) — it needs to see
// the message type first, since relayed frames arrive as a raw binary
// message on the same connection as the JSON envelopes used for
// everything else. Reads are never concurrent (only the owning
// handleHost/handleViewer goroutine reads), so this needs no locking,
// matching ReadJSON above.
func (c *safeConn) ReadMessage() (messageType int, data []byte, err error) {
	return c.conn.ReadMessage()
}

// Handler routes host and viewer WebSocket connections. Host connections
// register themselves and stay open (registration lives as long as the
// socket does). Viewer connections request the session list and relay
// SDP/ICE/screenshot-request messages to a specific host by session ID;
// the handler records which viewer socket is talking to which session
// (from the SessionID on its first offer/ICE/screenshot-request message)
// so the host's replies can be relayed back to the right viewer. Only
// one active viewer per session is tracked at a time, matching the
// MVP's one-admin-at-a-time design.
type Handler struct {
	mu       sync.Mutex
	reg      *Registry
	mux      *http.ServeMux
	hosts    map[string]*safeConn // sessionID -> host socket
	viewers  map[string]*safeConn // sessionID -> viewer socket
	relaysMu sync.Mutex
	relays   map[string]*FrameBroadcaster // sessionID -> broadcaster
}

func NewHandler(reg *Registry) *Handler {
	h := &Handler{
		reg:     reg,
		mux:     http.NewServeMux(),
		hosts:   make(map[string]*safeConn),
		viewers: make(map[string]*safeConn),
		relays:  make(map[string]*FrameBroadcaster),
	}
	h.mux.HandleFunc("/ws/host", h.handleHost)
	h.mux.HandleFunc("/ws/viewer", h.handleViewer)
	h.mux.HandleFunc("GET /stream/{session_id}", h.handleStream)
	return h
}

// broadcaster returns the session's FrameBroadcaster, creating it on
// first use. One broadcaster per session lives for the process lifetime
// of that session's registration — cheap enough (a map and a mutex) not
// to bother tearing down on host disconnect.
func (h *Handler) broadcaster(sessionID string) *FrameBroadcaster {
	h.relaysMu.Lock()
	defer h.relaysMu.Unlock()
	b, ok := h.relays[sessionID]
	if !ok {
		b = NewFrameBroadcaster()
		h.relays[sessionID] = b
	}
	return b
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) setHost(sessionID string, conn *safeConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hosts[sessionID] = conn
}

func (h *Handler) removeHost(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.hosts, sessionID)
}

func (h *Handler) setViewer(sessionID string, conn *safeConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.viewers[sessionID] = conn
}

func (h *Handler) hostConn(sessionID string) *safeConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hosts[sessionID]
}

func (h *Handler) viewerConn(sessionID string) *safeConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.viewers[sessionID]
}

func (h *Handler) handleHost(w http.ResponseWriter, r *http.Request) {
	rawConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := newSafeConn(rawConn)
	defer conn.Close()

	var sessionID string
	for {
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if sessionID != "" {
				h.reg.Unregister(sessionID)
				h.removeHost(sessionID)
				log.Printf("signaling: host session %s disconnected (%v)", sessionID, err)
			}
			return
		}

		// Relayed HTTP/MJPEG-fallback frames ride this same connection as
		// raw binary WebSocket messages instead of JSON envelopes — see
		// runFrameRelayLoop/WriteBinary in internal/hostapp — so the frame
		// itself never needs parsing, just handing to the broadcaster.
		if messageType == websocket.BinaryMessage {
			if sessionID != "" {
				h.broadcaster(sessionID).Publish(data)
			}
			continue
		}

		var env proto.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			log.Printf("signaling: bad envelope from host: %v", err)
			continue
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
		case proto.MsgAnswer, proto.MsgICECandidate, proto.MsgScreenshot:
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
	rawConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := newSafeConn(rawConn)
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
		case proto.MsgOffer, proto.MsgICECandidate, proto.MsgRequestScreenshot, proto.MsgInputEvent, proto.MsgOverlayMessage:
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
