# Remote Desktop Tool (AnyDesk-lite) Implementation Plan

> **For Claude:** Use `${SUPERPOWERS_SKILLS_ROOT}/skills/collaboration/executing-plans/SKILL.md` to implement this plan task-by-task.

**Goal:** Build a minimal Windows-only remote-desktop tool in Go: a signaling/relay server (deployed on the user's VPS) plus a Host agent and a Viewer app that connect P2P over WebRTC, with view+control, a safety toggle for mouse/keyboard input, and a fading on-screen text overlay the admin can push to the host's cursor.

**Architecture:** Three binaries share one Go module. `cmd/server` is a WebSocket signaling server (session registry + SDP/ICE relay) that runs on Linux (the VPS), alongside a standard `coturn` TURN/STUN server for NAT traversal. `cmd/host` and `cmd/viewer` run on Windows, connect out to the signaling server, negotiate a `pion/webrtc` PeerConnection per session, and exchange screen frames (JPEG over an unreliable DataChannel) and control messages (input events, overlay text) over a second reliable DataChannel. All Windows-native functionality (screen capture, input injection, the layered overlay window) is implemented with direct `golang.org/x/sys/windows` syscalls — no cgo — so both Windows binaries can be cross-compiled from any OS. The Viewer's GUI is built with Fyne, which *does* require cgo/OpenGL, so it needs `fyne-cross` (Docker) or a native Windows build machine — this is called out explicitly in Task 1.

**Tech Stack:** Go 1.26, `github.com/pion/webrtc/v4`, `github.com/gorilla/websocket`, `fyne.io/fyne/v2`, `github.com/kbinani/screenshot`, `golang.org/x/sys/windows`, `coturn` (system package, not Go code).

---

## Important constraint to resolve before Task 8

You are developing on **macOS (darwin/arm64)**. `cmd/server` is pure Go and cross-compiles trivially (`GOOS=linux go build`). `cmd/host` and `cmd/viewer` use `golang.org/x/sys/windows` syscalls only — also cross-compiles fine with plain `GOOS=windows GOARCH=amd64 go build`, no cgo needed. **However**, `cmd/viewer` depends on Fyne, which uses cgo + OpenGL bindings. Cross-compiling a cgo binary for Windows from macOS requires a mingw-w64 toolchain — Task 1 sets this up via `fyne-cross` (Docker-based), which is the realistic path unless you have a physical/VM Windows machine to build on directly. Confirm Docker Desktop is installed before Task 1, or plan to do final Viewer builds on a Windows box.

---

### Task 1: Project scaffolding + build environment

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `Makefile`
- Create: `cmd/server/main.go` (stub)
- Create: `cmd/host/main.go` (stub)
- Create: `cmd/viewer/main.go` (stub)

**Step 1: Init module and directories**

```bash
go mod init rdpAiAnswer
mkdir -p cmd/server cmd/host cmd/viewer
mkdir -p internal/proto internal/signaling internal/hotkey internal/overlay internal/capture internal/input internal/webrtcconn
```

**Step 2: Add dependencies**

```bash
go get github.com/pion/webrtc/v4
go get github.com/gorilla/websocket
go get fyne.io/fyne/v2
go get github.com/kbinani/screenshot
go get golang.org/x/sys
```

**Step 3: Write `.gitignore`**

```
/bin/
*.exe
.DS_Store
.worktrees/
worktrees/
```

**Step 4: Write three stub mains** (each just prints its name, proves the module builds and each binary is independently buildable):

`cmd/server/main.go`:
```go
package main

import "fmt"

func main() {
	fmt.Println("rdp-server starting")
}
```

`cmd/host/main.go` and `cmd/viewer/main.go`: same pattern with "rdp-host starting" / "rdp-viewer starting".

**Step 5: Write `Makefile`**

```makefile
.PHONY: server host viewer viewer-cross

server:
	GOOS=linux GOARCH=amd64 go build -o bin/rdp-server ./cmd/server

host:
	GOOS=windows GOARCH=amd64 go build -o bin/rdp-host.exe ./cmd/host

# viewer uses Fyne (cgo) — plain cross-compile will fail on macOS without a
# mingw toolchain. Use fyne-cross (Docker) instead:
viewer-cross:
	go run github.com/fyne-io/fyne-cross@latest windows -arch=amd64 -app-id=com.rdpaianswer.viewer ./cmd/viewer

viewer:
	go build -o bin/rdp-viewer ./cmd/viewer
```

**Step 6: Verify server and host cross-compile cleanly**

Run: `make server && make host`
Expected: `bin/rdp-server` and `bin/rdp-host.exe` produced with no errors — proves the no-cgo constraint holds.

**Step 7: Verify viewer builds natively on this machine** (Fyne needs a display/OpenGL context to build its native deps, but a plain `go build` for the host OS should succeed — this only proves your toolchain has a C compiler for Fyne, not that Windows cross-compile works)

Run: `make viewer`
Expected: `bin/rdp-viewer` produced. If this fails with a cgo/compiler error, install Xcode Command Line Tools (`xcode-select --install`) first.

**Step 8: Try the real target — Windows cross-compile via fyne-cross**

Run: `make viewer-cross`
Expected: requires Docker Desktop running; produces `fyne-cross/bin/windows-amd64/rdp-viewer.exe`. If Docker isn't available, note this as a blocker to resolve before Task 12 (you can develop/test Tasks 1–11 without it and only need working Windows Viewer builds once you reach the GUI tasks).

**Step 9: Commit**

```bash
git add go.mod go.sum .gitignore Makefile cmd
git commit -m "chore: scaffold three-binary Go module"
```

---

### Task 2: Shared protocol messages

**Files:**
- Create: `internal/proto/messages.go`
- Test: `internal/proto/messages_test.go`

This package defines every message type that crosses the wire — both over the signaling WebSocket (JSON) and over the WebRTC DataChannels (JSON with a 4-byte big-endian length prefix, since DataChannel is message-based but we want one framing scheme everywhere for simplicity).

**Step 1: Write the failing test**

```go
// internal/proto/messages_test.go
package proto

import "testing"

func TestEncodeDecodeFrame_RoundTrip(t *testing.T) {
	original := InputEvent{
		Kind: InputMouseMove,
		X:    100,
		Y:    250,
	}
	encoded, err := EncodeFrame(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	decoded, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	got, ok := decoded.(InputEvent)
	if !ok {
		t.Fatalf("decoded type = %T, want InputEvent", decoded)
	}
	if got != original {
		t.Fatalf("got %+v, want %+v", got, original)
	}
}

func TestOverlayMessage_ValidateFadeSeconds(t *testing.T) {
	tests := []struct {
		name    string
		fade    float64
		wantErr bool
	}{
		{"default", 2.0, false},
		{"one decimal", 3.5, false},
		{"zero", 0.0, true},
		{"negative", -1.0, true},
		{"too many decimals gets rounded not rejected", 2.34, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := OverlayMessage{Text: "hi", FadeSeconds: tt.fade}
			err := msg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOverlayMessage_RoundsFadeToOneDecimal(t *testing.T) {
	msg := OverlayMessage{Text: "hi", FadeSeconds: 2.347}
	msg.Normalize()
	if msg.FadeSeconds != 2.3 {
		t.Fatalf("FadeSeconds = %v, want 2.3", msg.FadeSeconds)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/proto/...`
Expected: FAIL — `EncodeFrame`, `DecodeFrame`, `InputEvent`, `OverlayMessage` undefined.

**Step 3: Write implementation**

```go
// internal/proto/messages.go
package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// --- Signaling messages (server <-> host/viewer, over WebSocket JSON) ---

type MsgType string

const (
	MsgRegisterHost   MsgType = "register_host"
	MsgListSessions   MsgType = "list_sessions"
	MsgSessionList    MsgType = "session_list"
	MsgConnectRequest MsgType = "connect_request"
	MsgOffer          MsgType = "offer"
	MsgAnswer         MsgType = "answer"
	MsgICECandidate   MsgType = "ice_candidate"
)

type Envelope struct {
	Type      MsgType         `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type RegisterHost struct {
	Name string `json:"name"`
}

type SessionInfo struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	Online    bool   `json:"online"`
}

type SessionList struct {
	Sessions []SessionInfo `json:"sessions"`
}

type SDPMessage struct {
	SDP string `json:"sdp"`
}

type ICECandidateMessage struct {
	Candidate string `json:"candidate"`
	SDPMid    string `json:"sdp_mid"`
	SDPMLine  uint16 `json:"sdp_mline_index"`
}

// --- DataChannel messages (host <-> viewer, length-prefixed JSON frames) ---

type FrameKind uint8

const (
	FrameKindInputEvent FrameKind = iota + 1
	FrameKindOverlayMessage
	FrameKindScreenFrame
)

type InputEventKind string

const (
	InputMouseMove   InputEventKind = "mouse_move"
	InputMouseDown   InputEventKind = "mouse_down"
	InputMouseUp     InputEventKind = "mouse_up"
	InputMouseWheel  InputEventKind = "mouse_wheel"
	InputKeyDown     InputEventKind = "key_down"
	InputKeyUp       InputEventKind = "key_up"
)

type InputEvent struct {
	Kind   InputEventKind `json:"kind"`
	X      int32          `json:"x,omitempty"`
	Y      int32          `json:"y,omitempty"`
	Button uint8          `json:"button,omitempty"`
	Delta  int32          `json:"delta,omitempty"`
	KeyVK  uint16         `json:"key_vk,omitempty"`
}

type OverlayMessage struct {
	Text        string  `json:"text"`
	FadeSeconds float64 `json:"fade_seconds"`
}

func (o OverlayMessage) Validate() error {
	if o.Text == "" {
		return errors.New("overlay message text must not be empty")
	}
	if o.FadeSeconds <= 0 {
		return errors.New("fade_seconds must be > 0")
	}
	return nil
}

// Normalize rounds FadeSeconds to one decimal place, matching the UI's
// tenths-precision stepper.
func (o *OverlayMessage) Normalize() {
	o.FadeSeconds = math.Round(o.FadeSeconds*10) / 10
}

type ScreenFrame struct {
	JPEG []byte `json:"jpeg"`
	Seq  uint32 `json:"seq"`
}

// --- Framing: [1 byte kind][4 byte big-endian length][JSON payload] ---

func EncodeFrame(v any) ([]byte, error) {
	var kind FrameKind
	switch v.(type) {
	case InputEvent:
		kind = FrameKindInputEvent
	case OverlayMessage:
		kind = FrameKindOverlayMessage
	case ScreenFrame:
		kind = FrameKindScreenFrame
	default:
		return nil, fmt.Errorf("proto: unsupported frame type %T", v)
	}

	payload, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 5+len(payload))
	buf[0] = byte(kind)
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	return buf, nil
}

func DecodeFrame(data []byte) (any, error) {
	if len(data) < 5 {
		return nil, errors.New("proto: frame too short")
	}
	kind := FrameKind(data[0])
	length := binary.BigEndian.Uint32(data[1:5])
	if int(length) != len(data)-5 {
		return nil, fmt.Errorf("proto: length mismatch: header says %d, got %d", length, len(data)-5)
	}
	payload := data[5:]

	switch kind {
	case FrameKindInputEvent:
		var v InputEvent
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, err
		}
		return v, nil
	case FrameKindOverlayMessage:
		var v OverlayMessage
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, err
		}
		return v, nil
	case FrameKindScreenFrame:
		var v ScreenFrame
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, err
		}
		return v, nil
	default:
		return nil, fmt.Errorf("proto: unknown frame kind %d", kind)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/proto/... -v`
Expected: PASS, all subtests green.

**Step 5: Commit**

```bash
git add internal/proto
git commit -m "feat: add shared wire protocol with length-prefixed framing"
```

---

### Task 3: Session registry (server-side)

**Files:**
- Create: `internal/signaling/registry.go`
- Test: `internal/signaling/registry_test.go`

**Step 1: Write the failing test**

```go
// internal/signaling/registry_test.go
package signaling

import (
	"sync"
	"testing"
)

func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewRegistry()
	id := r.Register("PC-OFFICE-1")

	sessions := r.List()
	if len(sessions) != 1 {
		t.Fatalf("List() len = %d, want 1", len(sessions))
	}
	if sessions[0].SessionID != id || sessions[0].Name != "PC-OFFICE-1" || !sessions[0].Online {
		t.Fatalf("unexpected session: %+v", sessions[0])
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	id := r.Register("PC-HOME")
	r.Unregister(id)

	if len(r.List()) != 0 {
		t.Fatalf("List() after Unregister = %d, want 0", len(r.List()))
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := r.Register("concurrent")
			r.List()
			r.Unregister(id)
		}()
	}
	wg.Wait()
	if len(r.List()) != 0 {
		t.Fatalf("List() after concurrent churn = %d, want 0", len(r.List()))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/signaling/... -run TestRegistry`
Expected: FAIL — `NewRegistry` undefined.

**Step 3: Write implementation**

```go
// internal/signaling/registry.go
package signaling

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"rdpAiAnswer/internal/proto"
)

type hostEntry struct {
	name string
}

type Registry struct {
	mu    sync.RWMutex
	hosts map[string]hostEntry
}

func NewRegistry() *Registry {
	return &Registry{hosts: make(map[string]hostEntry)}
}

func (r *Registry) Register(name string) string {
	id := newSessionID()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts[id] = hostEntry{name: name}
	return id
}

func (r *Registry) Unregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hosts, sessionID)
}

func (r *Registry) List() []proto.SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]proto.SessionInfo, 0, len(r.hosts))
	for id, h := range r.hosts {
		out = append(out, proto.SessionInfo{SessionID: id, Name: h.name, Online: true})
	}
	return out
}

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/signaling/... -run TestRegistry -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/signaling/registry.go internal/signaling/registry_test.go
git commit -m "feat: add thread-safe session registry"
```

---

### Task 4: Signaling WebSocket server

**Files:**
- Create: `internal/signaling/handler.go`
- Test: `internal/signaling/handler_test.go`
- Modify: `cmd/server/main.go`

**Step 1: Write the failing test** (host registers, viewer lists sessions and sees it)

```go
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
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/signaling/... -run TestHandler`
Expected: FAIL — `NewHandler` undefined.

**Step 3: Write implementation**

```go
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
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/signaling/... -v`
Expected: PASS.

**Step 5: Wire into `cmd/server/main.go`**

```go
package main

import (
	"log"
	"net/http"

	"rdpAiAnswer/internal/signaling"
)

func main() {
	reg := signaling.NewRegistry()
	handler := signaling.NewHandler(reg)
	log.Println("rdp-server listening on :9000")
	log.Fatal(http.ListenAndServe(":9000", handler))
}
```

**Step 6: Commit**

```bash
git add internal/signaling cmd/server/main.go
git commit -m "feat: add signaling websocket server with session list endpoint"
```

*(Note: the SDP/ICE relay between a specific viewer and host is implemented in Task 7 once `internal/webrtcconn` defines the exact message shapes it needs — the plumbing above is the registration/listing half, which is fully testable now.)*

---

### Task 5: Hotkey combo parser

**Files:**
- Create: `internal/hotkey/hotkey.go`
- Test: `internal/hotkey/hotkey_test.go`

Pure logic, no OS dependency — used by the Viewer's rebind UI (Task 13) via Fyne key events, which already give us modifier + key name as strings/enums, so this package only deals with a normalized representation and (de)serialization for persistence.

**Step 1: Write the failing test**

```go
// internal/hotkey/hotkey_test.go
package hotkey

import "testing"

func TestCombo_String(t *testing.T) {
	c := Combo{Ctrl: true, Alt: true, Key: "C"}
	if got := c.String(); got != "Ctrl+Alt+C" {
		t.Fatalf("String() = %q, want %q", got, "Ctrl+Alt+C")
	}
}

func TestParseCombo(t *testing.T) {
	tests := []struct {
		in   string
		want Combo
	}{
		{"Ctrl+Alt+C", Combo{Ctrl: true, Alt: true, Key: "C"}},
		{"Shift+F1", Combo{Shift: true, Key: "F1"}},
		{"M", Combo{Key: "M"}},
	}
	for _, tt := range tests {
		got, err := ParseCombo(tt.in)
		if err != nil {
			t.Fatalf("ParseCombo(%q) error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseCombo(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseCombo_RequiresAKey(t *testing.T) {
	if _, err := ParseCombo("Ctrl+Alt"); err == nil {
		t.Fatal("expected error for modifiers-only combo")
	}
}

func TestCombo_Matches(t *testing.T) {
	configured := Combo{Ctrl: true, Alt: true, Key: "C"}
	pressed := Combo{Ctrl: true, Alt: true, Key: "C"}
	if !configured.Matches(pressed) {
		t.Fatal("expected exact combo to match")
	}
	if configured.Matches(Combo{Ctrl: true, Key: "C"}) {
		t.Fatal("expected missing modifier to not match")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/hotkey/...`
Expected: FAIL — package/types undefined.

**Step 3: Write implementation**

```go
// internal/hotkey/hotkey.go
package hotkey

import (
	"errors"
	"strings"
)

// Combo is a normalized hotkey representation: a set of modifier flags
// plus exactly one non-modifier key. It is intentionally OS-agnostic —
// the Viewer's Fyne key handler maps *fyne.KeyEvent into a Combo, and the
// same struct is persisted to disk as its String() form.
type Combo struct {
	Ctrl  bool
	Alt   bool
	Shift bool
	Key   string // canonical key name, e.g. "C", "F1"
}

func (c Combo) String() string {
	var parts []string
	if c.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if c.Alt {
		parts = append(parts, "Alt")
	}
	if c.Shift {
		parts = append(parts, "Shift")
	}
	parts = append(parts, c.Key)
	return strings.Join(parts, "+")
}

func (c Combo) Matches(pressed Combo) bool {
	return c == pressed
}

func ParseCombo(s string) (Combo, error) {
	parts := strings.Split(s, "+")
	if len(parts) == 0 {
		return Combo{}, errors.New("hotkey: empty combo")
	}
	var c Combo
	key := parts[len(parts)-1]
	if key == "" {
		return Combo{}, errors.New("hotkey: missing key")
	}
	if key == "Ctrl" || key == "Alt" || key == "Shift" {
		return Combo{}, errors.New("hotkey: combo must end in a non-modifier key")
	}
	c.Key = key
	for _, mod := range parts[:len(parts)-1] {
		switch mod {
		case "Ctrl":
			c.Ctrl = true
		case "Alt":
			c.Alt = true
		case "Shift":
			c.Shift = true
		default:
			return Combo{}, errors.New("hotkey: unknown modifier " + mod)
		}
	}
	return c, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/hotkey/... -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/hotkey
git commit -m "feat: add OS-agnostic hotkey combo parser for rebind UI"
```

---

### Task 6: Overlay fade timer logic

**Files:**
- Create: `internal/overlay/fade.go`
- Test: `internal/overlay/fade_test.go`

Pure logic (no Windows API yet — that's Task 10). This is what decides *when* the overlay window should be destroyed and what opacity it should render at, given the configurable fade duration (default 2.0s, tenths precision, validated already by `proto.OverlayMessage`).

**Step 1: Write the failing test**

```go
// internal/overlay/fade_test.go
package overlay

import (
	"testing"
	"time"
)

func TestFadeTimer_OpacityAtStart(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if got := f.Opacity(0); got != 1.0 {
		t.Fatalf("Opacity(0) = %v, want 1.0", got)
	}
}

func TestFadeTimer_OpacityAtEnd(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if got := f.Opacity(2 * time.Second); got != 0.0 {
		t.Fatalf("Opacity(total) = %v, want 0.0", got)
	}
}

func TestFadeTimer_OpacityHalfway(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if got := f.Opacity(1 * time.Second); got != 0.5 {
		t.Fatalf("Opacity(half) = %v, want 0.5", got)
	}
}

func TestFadeTimer_OpacityClampsPastEnd(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if got := f.Opacity(10 * time.Second); got != 0.0 {
		t.Fatalf("Opacity(past end) = %v, want 0.0", got)
	}
}

func TestFadeTimer_IsExpired(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if f.IsExpired(1900 * time.Millisecond) {
		t.Fatal("should not be expired just before total duration")
	}
	if !f.IsExpired(2 * time.Second) {
		t.Fatal("should be expired at total duration")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/overlay/... -run TestFadeTimer`
Expected: FAIL — `NewFadeTimer` undefined.

**Step 3: Write implementation**

```go
// internal/overlay/fade.go
package overlay

import "time"

// FadeTimer computes a linear opacity ramp from 1.0 down to 0.0 over a
// fixed total duration. The renderer (Task 10, Windows-only) polls
// Opacity(elapsed) on a ticker and calls SetLayeredWindowAttributes with
// the result; IsExpired tells it when to destroy the window.
type FadeTimer struct {
	total time.Duration
}

func NewFadeTimer(total time.Duration) *FadeTimer {
	return &FadeTimer{total: total}
}

func (f *FadeTimer) Opacity(elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 1.0
	}
	if elapsed >= f.total {
		return 0.0
	}
	return 1.0 - float64(elapsed)/float64(f.total)
}

func (f *FadeTimer) IsExpired(elapsed time.Duration) bool {
	return elapsed >= f.total
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/overlay/... -v`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/overlay/fade.go internal/overlay/fade_test.go
git commit -m "feat: add linear fade timer for overlay messages"
```

---

### Task 7: WebRTC peer connection helper + signaling relay wiring

**Files:**
- Create: `internal/webrtcconn/peer.go`
- Test: `internal/webrtcconn/peer_test.go`
- Modify: `internal/signaling/handler.go` (add viewer-to-host relay for offer/answer/ICE)
- Modify: `internal/signaling/handler_test.go`

**Step 1: Write the failing test** (two in-process PeerConnections, loopback ICE, verifies a DataChannel message round-trips — no real network/TURN needed since pion negotiates host candidates on loopback)

```go
// internal/webrtcconn/peer_test.go
package webrtcconn

import (
	"testing"
	"time"
)

func TestPeer_DataChannelRoundTrip(t *testing.T) {
	offerer, err := NewPeer(nil) // nil ICE servers = host-candidates-only, fine for a loopback test
	if err != nil {
		t.Fatalf("NewPeer offerer: %v", err)
	}
	defer offerer.Close()

	answerer, err := NewPeer(nil)
	if err != nil {
		t.Fatalf("NewPeer answerer: %v", err)
	}
	defer answerer.Close()

	received := make(chan []byte, 1)
	answerer.OnData(func(data []byte) { received <- data })

	offer, err := offerer.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	answer, err := answerer.AcceptOffer(offer)
	if err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}
	if err := offerer.AcceptAnswer(answer); err != nil {
		t.Fatalf("AcceptAnswer: %v", err)
	}

	if err := offerer.WaitConnected(5 * time.Second); err != nil {
		t.Fatalf("WaitConnected: %v", err)
	}

	if err := offerer.Send([]byte("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case data := <-received:
		if string(data) != "hello" {
			t.Fatalf("received %q, want %q", data, "hello")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for data channel message")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/webrtcconn/...`
Expected: FAIL — `NewPeer` undefined.

**Step 3: Write implementation**

```go
// internal/webrtcconn/peer.go
package webrtcconn

import (
	"errors"
	"time"

	"github.com/pion/webrtc/v4"
)

// Peer wraps a pion PeerConnection plus a single named DataChannel
// ("control") used for both directions of proto-framed messages
// (screen frames, input events, overlay messages — see internal/proto).
// Both Host and Viewer use this same type; whichever side calls
// CreateOffer first is the offerer.
type Peer struct {
	pc         *webrtc.PeerConnection
	dc         *webrtc.DataChannel
	onData     func([]byte)
	connected  chan struct{}
	connectedC bool
}

// iceServers of nil means host-candidates-only (fine for same-machine
// tests; production callers pass STUN/TURN URLs from config).
func NewPeer(iceServers []webrtc.ICEServer) (*Peer, error) {
	config := webrtc.Configuration{ICEServers: iceServers}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	p := &Peer{pc: pc, connected: make(chan struct{})}

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected && !p.connectedC {
			p.connectedC = true
			close(p.connected)
		}
	})

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		p.dc = dc
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if p.onData != nil {
				p.onData(msg.Data)
			}
		})
	})

	return p, nil
}

func (p *Peer) OnData(fn func([]byte)) { p.onData = fn }

func (p *Peer) CreateOffer() (webrtc.SessionDescription, error) {
	dc, err := p.pc.CreateDataChannel("control", nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	p.dc = dc
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if p.onData != nil {
			p.onData(msg.Data)
		}
	})

	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	<-gatherComplete
	return *p.pc.LocalDescription(), nil
}

func (p *Peer) AcceptOffer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	<-gatherComplete
	return *p.pc.LocalDescription(), nil
}

func (p *Peer) AcceptAnswer(answer webrtc.SessionDescription) error {
	return p.pc.SetRemoteDescription(answer)
}

func (p *Peer) WaitConnected(timeout time.Duration) error {
	select {
	case <-p.connected:
		return nil
	case <-time.After(timeout):
		return errors.New("webrtcconn: timed out waiting for connection")
	}
}

func (p *Peer) Send(data []byte) error {
	if p.dc == nil {
		return errors.New("webrtcconn: data channel not established")
	}
	return p.dc.Send(data)
}

func (p *Peer) Close() error {
	return p.pc.Close()
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/webrtcconn/... -v`
Expected: PASS (may take a few seconds for ICE gathering on loopback).

**Step 5: Add offer/answer/ICE relay to the signaling handler**

Extend `handler_test.go` with a test that dials host + viewer, has the viewer send `MsgOffer` with a `session_id`, and asserts the host socket receives it verbatim; then implement by tracking viewer sockets keyed by a generated connection ID and forwarding `MsgOffer`/`MsgAnswer`/`MsgICECandidate` between the two sockets identified by `Envelope.SessionID`. (Follow the same test-first pattern as Task 4 — write the relay test, watch it fail, implement `relay` methods on `Handler` that look up the peer socket in `h.hosts`/a new `h.viewers` map and call `WriteJSON` with the untouched envelope.)

**Step 6: Run full signaling test suite**

Run: `go test ./internal/signaling/... -v`
Expected: PASS.

**Step 7: Commit**

```bash
git add internal/webrtcconn internal/signaling
git commit -m "feat: add WebRTC peer helper and wire SDP/ICE relay through signaling server"
```

---

### Task 8: Host screen capture (Windows-only)

**Files:**
- Create: `internal/capture/capture_windows.go` (build tag `//go:build windows`)
- Create: `internal/capture/capture_windows_manualtest.go` (small throwaway `main` under `cmd/captest`, or a `_test.go` guarded the same way — see Step 3)

This cannot be unit-tested on macOS (no Windows display) and `kbinani/screenshot` needs an actual screen even on Windows, so verification here is manual, on a real Windows machine or VM.

**Step 1: Write the capture wrapper**

```go
//go:build windows

package capture

import (
	"bytes"
	"image/jpeg"

	"github.com/kbinani/screenshot"
)

// GrabPrimaryJPEG captures the primary display and returns it JPEG-encoded
// at the given quality (1-100). Called once per frame by the host's
// capture loop (Task 11).
func GrabPrimaryJPEG(quality int) ([]byte, error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
```

**Step 2: Write a tiny manual-verify command**

```go
// cmd/captest/main.go
//go:build windows

package main

import (
	"log"
	"os"

	"rdpAiAnswer/internal/capture"
)

func main() {
	data, err := capture.GrabPrimaryJPEG(80)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("capture_test.jpg", data, 0644); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote capture_test.jpg —", len(data), "bytes")
}
```

**Step 3: Manual verification (on Windows)**

Run: `GOOS=windows GOARCH=amd64 go build -o bin/captest.exe ./cmd/captest` (from macOS), copy `captest.exe` to a Windows machine, run it, then open `capture_test.jpg`.
Expected: a JPEG showing the current desktop.

**Step 4: Commit**

```bash
git add internal/capture cmd/captest
git commit -m "feat: add Windows screen capture via kbinani/screenshot"
```

---

### Task 9: Host input injection (Windows-only)

**Files:**
- Create: `internal/input/inject_windows.go` (build tag `windows`)

Implements `SendInput` directly via `golang.org/x/sys/windows` — no cgo, no third-party injector library, so it cross-compiles cleanly and stays auditable.

**Step 1: Write the implementation** (manual-verify only, same reasoning as Task 8 — there is no way to assert "the OS cursor moved" from a unit test)

```go
//go:build windows

package input

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Mirrors the Win32 MOUSEINPUT/KEYBDINPUT/INPUT structs from winuser.h.
// Field order and sizes must match exactly — this is the one place in the
// codebase where a typo silently corrupts a syscall instead of failing to
// compile.
const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseEventMove     = 0x0001
	mouseEventLeftDown = 0x0002
	mouseEventLeftUp   = 0x0004
	mouseEventAbsolute = 0x8000

	keyEventKeyUp = 0x0002
)

type mouseInput struct {
	dx, dy      int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type keybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type input struct {
	inputType uint32
	// union of mouseInput/keybdInput, padded to the larger member's size
	mi mouseInput
	_  [8]byte // padding so the struct is layout-compatible across mi/ki use
}

var (
	user32       = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

func sendRawInput(in input) error {
	size := unsafe.Sizeof(in)
	ret, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), size)
	if ret == 0 {
		return err
	}
	return nil
}

func MoveMouse(x, y int32) error {
	in := input{inputType: inputMouse, mi: mouseInput{
		dx: x, dy: y,
		dwFlags: mouseEventMove | mouseEventAbsolute,
	}}
	return sendRawInput(in)
}

func MouseButton(down bool) error {
	flag := uint32(mouseEventLeftDown)
	if !down {
		flag = mouseEventLeftUp
	}
	in := input{inputType: inputMouse, mi: mouseInput{dwFlags: flag}}
	return sendRawInput(in)
}

func KeyPress(vk uint16, down bool) error {
	var flags uint32
	if !down {
		flags = keyEventKeyUp
	}
	ki := keybdInput{wVk: vk, dwFlags: flags}
	in := input{inputType: inputKeyboard}
	// reinterpret mi field's memory as keybdInput since they share the union slot
	*(*keybdInput)(unsafe.Pointer(&in.mi)) = ki
	return sendRawInput(in)
}
```

*Note for the implementing engineer:* Go doesn't have real unions, so the struct above uses the `unsafe.Pointer` reinterpret trick to reuse the `mi` field's memory for keyboard input. If this feels fragile (it is, slightly), the safer alternative is two separate `SendInput` wrapper functions each building their own correctly-sized byte buffer with `binary.Write` — do that instead if the unsafe cast causes any observed corruption during manual testing.

**Step 2: Manual verification (on Windows)**

Write a throwaway `cmd/inputtest/main.go` that calls `input.MoveMouse(500, 500)` then `input.KeyPress(0x41 /* VK_A */, true)` / `false`, build for Windows, run on a real Windows box, confirm the cursor jumps and an "a" appears in a focused text field.

**Step 3: Commit**

```bash
git add internal/input
git commit -m "feat: add Windows SendInput wrapper for mouse/keyboard injection"
```

---

### Task 10: Host overlay window (Windows-only)

**Files:**
- Create: `internal/overlay/window_windows.go` (build tag `windows`)

This is the trickiest native piece: a borderless, click-through, always-on-top window showing text near the cursor, fading via `FadeTimer` from Task 6.

**Step 1: Write the implementation**

```go
//go:build windows

package overlay

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wsExLayered    = 0x00080000
	wsExTransparent = 0x00000020
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	lwaAlpha        = 0x00000002
	swShow          = 5
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procCreateWindowEx           = user32.NewProc("CreateWindowExW")
	procSetLayeredWindowAttrs    = user32.NewProc("SetLayeredWindowAttributes")
	procShowWindow               = user32.NewProc("ShowWindow")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procDefWindowProc            = user32.NewProc("DefWindowProcW")
	procRegisterClass            = user32.NewProc("RegisterClassExW")
)

type point struct{ X, Y int32 }

// ShowMessage creates a click-through layered window near the current
// cursor position showing text, fades it out over fadeDuration using
// FadeTimer, and destroys it when done. Blocks the calling goroutine for
// the duration of the fade — callers (Task 11's message handler) should
// run this in its own goroutine per incoming overlay message.
func ShowMessage(text string, fadeDuration time.Duration) error {
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))

	hwnd, err := createOverlayWindow(text, cursor.X, cursor.Y)
	if err != nil {
		return err
	}
	defer procDestroyWindow.Call(hwnd)

	procShowWindow.Call(hwnd, swShow)

	timer := NewFadeTimer(fadeDuration)
	start := time.Now()
	ticker := time.NewTicker(33 * time.Millisecond) // ~30fps fade
	defer ticker.Stop()

	for range ticker.C {
		elapsed := time.Since(start)
		opacity := timer.Opacity(elapsed)
		alpha := uintptr(opacity * 255)
		procSetLayeredWindowAttrs.Call(hwnd, 0, alpha, lwaAlpha)
		if timer.IsExpired(elapsed) {
			return nil
		}
	}
	return nil
}

// createOverlayWindow registers a minimal window class on first use and
// creates a WS_EX_LAYERED|WS_EX_TRANSPARENT|WS_EX_TOPMOST popup window
// sized to fit text, using GDI DrawText/ExtTextOut against the window's
// DC. Full class registration + WM_PAINT text rendering is ~80-120 lines
// of standard Win32 boilerplate (RegisterClassEx, WNDCLASSEX struct,
// WM_PAINT -> BeginPaint/DrawText/EndPaint) — implement it here following
// any Win32-in-Go layered-window tutorial; the shape (function signature,
// return hwnd uintptr) is what the rest of this package depends on.
func createOverlayWindow(text string, x, y int32) (uintptr, error) {
	panic("TODO: implement in Task 10, Step 2 — see doc comment above")
}
```

**Step 2: Implement `createOverlayWindow`**

This is real Win32 boilerplate (register a window class, handle `WM_PAINT` to draw the text with `DrawTextW`, size the window to the text). Budget extra time for this specific function — it's the single most fiddly piece of the whole project because Go has no MFC/WinAPI helper layer. Reference any "Win32 layered window in Go, no cgo" example for the exact `WNDCLASSEX`/`CreateWindowExW` call shapes; keep the function signature `func createOverlayWindow(text string, x, y int32) (uintptr, error)` so `ShowMessage` above doesn't need to change.

**Step 3: Manual verification (on Windows)**

Throwaway `cmd/overlaytest/main.go` calling `overlay.ShowMessage("привет", 2*time.Second)`. Build for Windows, run on a real machine, confirm: text appears near the cursor, no border/titlebar, stays on top of other windows, clicking through it interacts with whatever is underneath, and it fades and disappears after ~2s.

**Step 4: Commit**

```bash
git add internal/overlay/window_windows.go
git commit -m "feat: add click-through fading overlay window for admin messages"
```

---

### Task 11: Host agent wiring (`cmd/host`)

**Files:**
- Modify: `cmd/host/main.go`
- Create: `internal/hostapp/app.go`

**Step 1: Implement the host application loop**

```go
// internal/hostapp/app.go
package hostapp

import (
	"log"
	"time"

	"github.com/gorilla/websocket"
	"rdpAiAnswer/internal/capture"
	"rdpAiAnswer/internal/input"
	"rdpAiAnswer/internal/overlay"
	"rdpAiAnswer/internal/proto"
	"rdpAiAnswer/internal/webrtcconn"
)

type Config struct {
	SignalingURL string // e.g. wss://your-vps:9000/ws/host
	Name         string
	ICEServers   []string // STUN/TURN URLs from Task 15's coturn deployment
}

// Run connects to the signaling server, registers as a host, and for each
// viewer that connects, streams JPEG screen frames and applies incoming
// input/overlay messages. Blocks until conn fails or ctx is cancelled by
// the caller (cmd/host/main.go handles OS signal shutdown).
func Run(cfg Config) error {
	// 1. Dial signaling WS, send proto.MsgRegisterHost{Name: cfg.Name}.
	// 2. On MsgOffer from a viewer (relayed by Task 7's handler), create a
	//    webrtcconn.Peer, AcceptOffer, send back MsgAnswer, relay any
	//    MsgICECandidate both directions.
	// 3. Once Peer.WaitConnected succeeds, start two goroutines:
	//    a) capture loop: every ~100ms (10fps to start — tune after
	//       measuring bandwidth), capture.GrabPrimaryJPEG, wrap in
	//       proto.ScreenFrame, proto.EncodeFrame, Peer.Send.
	//    b) Peer.OnData handler: proto.DecodeFrame the incoming bytes;
	//       switch on type — InputEvent -> input.MoveMouse/MouseButton/
	//       KeyPress; OverlayMessage -> go overlay.ShowMessage(msg.Text,
	//       time.Duration(msg.FadeSeconds*float64(time.Second))).
	// Implement this as a straight sequential function once the pieces
	// above compile — every dependency it calls already has its own
	// tests/manual verification from Tasks 2-10, so this task is
	// integration wiring, not new logic.
	panic("TODO: implement per the steps above")
}
```

**Step 2: Wire `cmd/host/main.go`**

```go
package main

import (
	"flag"
	"log"

	"rdpAiAnswer/internal/hostapp"
)

func main() {
	name := flag.String("name", "unnamed-host", "display name shown in the viewer's session list")
	server := flag.String("server", "wss://localhost:9000/ws/host", "signaling server URL")
	flag.Parse()

	if err := hostapp.Run(hostapp.Config{SignalingURL: *server, Name: *name}); err != nil {
		log.Fatal(err)
	}
}
```

**Step 3: Manual verification (on Windows, against local `cmd/server`)**

Run `bin/rdp-server` locally, run `rdp-host.exe -name TEST -server ws://localhost:9000/ws/host` on a Windows VM, and confirm (via a temporary `curl`/websocket test client, since Viewer isn't built yet) that `MsgListSessions` returns the `TEST` session. Full end-to-end screen viewing is verified once Task 13 (Viewer control window) exists.

**Step 4: Commit**

```bash
git add internal/hostapp cmd/host/main.go
git commit -m "feat: wire host agent — capture, input injection, overlay, signaling"
```

---

### Task 12: Viewer session list screen (Fyne)

**Files:**
- Create: `internal/viewerapp/sessionlist.go`
- Modify: `cmd/viewer/main.go`

**Step 1: Implement the session list window**

```go
// internal/viewerapp/sessionlist.go
package viewerapp

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"rdpAiAnswer/internal/proto"
)

// ShowSessionList opens the main window: a refreshable list of online
// hosts fetched from the signaling server, opening a control window
// (Task 13) on click.
func ShowSessionList(fyneApp fyne.App, signalingURL string) {
	w := fyneApp.NewWindow("RDP-Tool — Sessions")

	list := widget.NewList(
		func() int { return len(currentSessions) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(currentSessions[i].Name)
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		OpenControlWindow(fyneApp, signalingURL, currentSessions[i])
	}

	refresh := widget.NewButton("Refresh", func() {
		fetchSessions(signalingURL)
		list.Refresh()
	})

	w.SetContent(container.NewBorder(nil, refresh, nil, nil, list))
	w.Resize(fyne.NewSize(400, 300))
	w.Show()
}

var currentSessions []proto.SessionInfo

func fetchSessions(signalingURL string) {
	// Dial signalingURL + "/ws/viewer", send MsgListSessions, read
	// MsgSessionList response, assign to currentSessions. Keep the
	// connection open and store it for reuse by Task 13 (the control
	// window reuses this same WS connection to send MsgOffer for the
	// selected session rather than opening a second one).
}
```

**Step 2: Wire `cmd/viewer/main.go`**

```go
package main

import (
	"flag"

	"fyne.io/fyne/v2/app"
	"rdpAiAnswer/internal/viewerapp"
)

func main() {
	server := flag.String("server", "ws://localhost:9000", "signaling server base URL")
	flag.Parse()

	a := app.New()
	viewerapp.ShowSessionList(a, *server)
	a.Run()
}
```

**Step 3: Manual verification**

Run `make viewer` (native build is enough to check the UI renders — full Windows behavior is checked once cross-compiled via `fyne-cross` per Task 1). Start `bin/rdp-server` and a fake host registration (reuse the throwaway test client from Task 11), click "Refresh", confirm the session appears in the list.

**Step 4: Commit**

```bash
git add internal/viewerapp cmd/viewer/main.go
git commit -m "feat: add Fyne session list screen"
```

---

### Task 13: Viewer control window (video + toggles + hotkey rebind + message form)

**Files:**
- Create: `internal/viewerapp/controlwindow.go`

**Step 1: Implement the control window**

Structure, matching the approved wireframe:
- Left: `canvas.Image` updated on each incoming `proto.ScreenFrame` (decode JPEG bytes to `image.Image`, `canvas.NewImageFromImage`, `img.Refresh()`).
- Right sidebar: two `widget.Check` toggles ("Mouse", "Keyboard"), each defaulting to **off**, each with a small label below showing the current `hotkey.Combo.String()` and a "Rebind" button.
- Rebind flow: clicking "Rebind" sets a `awaitingRebind *string` flag naming which toggle is being rebound; the window's `Canvas().SetOnTypedKey`/`(desktop.Canvas).SetOnKeyDown` handler, when `awaitingRebind != nil`, builds a `hotkey.Combo` from the current modifier state + key, calls `hotkey.ParseCombo`-compatible construction, stores it, clears `awaitingRebind`, updates the label.
- Message form: `widget.Entry` for text, a numeric stepper for fade seconds (use `widget.NewEntry` + validator restricting to one decimal, or `widget.NewSlider` with 0.1 step if Fyne's widget set at your installed version has a suitable numeric stepper — check `fyne.io/fyne/v2/widget` docs for the exact API available in your `go.mod` version) defaulting to `2.0`, "Send" button building a `proto.OverlayMessage{Text: ..., FadeSeconds: ...}`, calling `.Normalize()`, `.Validate()`, and on success `proto.EncodeFrame` + `Peer.Send`.
- When a toggle is on, the window's mouse-move/click/key handlers (Fyne's `desktop.Mouseable`/`desktop.Hoverable` or a raw `canvas.Image` wrapped with a custom `fyne.CanvasObject` implementing `Dragged`/`Tapped`) build `proto.InputEvent`s and send them over the Peer.

**Step 2: Manual verification (end-to-end, needs two Windows machines/VMs on different networks + the VPS running `cmd/server` and `coturn`)**

1. Start `rdp-server` and `coturn` on the VPS (Task 15).
2. Run `rdp-host.exe` on Host machine.
3. Run `rdp-viewer.exe` on Admin machine, click the session, confirm the screen appears.
4. Toggle "Mouse" on, move mouse over the video, confirm the Host's cursor follows.
5. Toggle "Keyboard" on, type, confirm keystrokes land on the Host.
6. Click "Rebind" under Mouse, press a new combo, confirm the label updates and the old combo no longer toggles it.
7. Type a message, set fade to `2.0`, click Send, confirm text appears at the Host's cursor, borderless, on top, click-through, and fades out in ~2 seconds.

**Step 3: Commit**

```bash
git add internal/viewerapp/controlwindow.go
git commit -m "feat: add viewer control window with input toggles, hotkey rebind, overlay messaging"
```

---

### Task 14: Deploy signaling server + TURN/STUN on the VPS

**Files:**
- Create: `docs/deploy/vps-setup.md`

**Step 1: Write deployment doc covering:**
- Building `rdp-server` for Linux (`make server`), copying to VPS, running as a `systemd` service on port 9000 (behind TLS via a reverse proxy like Caddy/nginx for `wss://`, since browsers/Fyne's websocket client will be happier with TLS and it's needed for the P2P handshake to traverse some corporate proxies anyway).
- Installing `coturn` (`apt install coturn`), minimal `/etc/turnserver.conf`: `listening-port=3478`, `fingerprint`, `lt-cred-mech`, a static `user=rdp:<password>`, `realm=<vps-ip-or-domain>`. Enable and start the systemd service.
- The exact `webrtc.ICEServer` values `cmd/host`/`cmd/viewer` need to pass into `webrtcconn.NewPeer` (`stun:<vps>:3478` and `turn:<vps>:3478` with the configured username/credential) — wire these as `-ice-server` flags on both binaries rather than hardcoding.

**Step 2: Deploy and smoke-test**

Run: from the VPS, `systemctl status rdp-server coturn` — both `active (running)`.
Expected output confirms both services up; then re-run Task 13's end-to-end checklist using the real VPS `wss://` URL instead of `localhost`.

**Step 3: Commit**

```bash
git add docs/deploy/vps-setup.md
git commit -m "docs: add VPS deployment guide for signaling + TURN server"
```

---

## Summary of what's genuinely hard here

Tasks 2–7 (protocol, registry, signaling server, hotkey parser, fade timer, WebRTC peer helper) are ordinary Go, fully unit-tested, and should go quickly for someone at your level. Tasks 8–10 (native Windows capture/input/overlay) and Task 13 (Fyne control window with live video + custom input capture) are where most of the real time goes — they're un-unit-testable, Windows-only, and Task 10 in particular involves raw Win32 window-class boilerplate that Go has no shortcuts for. Realistically this is **several focused days**, not hours, concentrated almost entirely in the native-Windows and Fyne-GUI tasks rather than the networking/protocol layer.
