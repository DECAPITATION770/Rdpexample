//go:build windows

// Package hostapp wires together the Windows-only pieces (screen capture,
// input injection, overlay messages) with the signaling connection and a
// WebRTC PeerConnection per viewer. It only builds for windows since it
// depends on internal/capture, internal/input, and internal/overlay,
// which are all windows-only — matching cmd/host, the only thing that
// imports this package.
package hostapp

import (
	"encoding/json"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"rdpAiAnswer/internal/capture"
	"rdpAiAnswer/internal/input"
	"rdpAiAnswer/internal/overlay"
	"rdpAiAnswer/internal/proto"
	"rdpAiAnswer/internal/webrtcconn"
)

// safeConn wraps the signaling WebSocket with a write mutex. Multiple
// goroutines write to it concurrently — handleOffer's answer, per-session
// screenshot responses — and gorilla/websocket only allows one concurrent
// writer per connection. Reads happen only on Run's single loop, so
// ReadJSON needs no locking.
type safeConn struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (c *safeConn) WriteJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

// WriteBinary sends data as a raw WebSocket binary message — used only
// by the frame-relay path (see runFrameRelayLoop), which the signaling
// server distinguishes from JSON envelopes by WebSocket message type
// rather than an in-payload marker. Binary avoids both the ~33% size
// overhead of JSON's automatic base64-encoding of []byte fields and the
// JSON marshal/unmarshal CPU cost on every single frame.
func (c *safeConn) WriteBinary(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (c *safeConn) ReadJSON(v any) error { return c.conn.ReadJSON(v) }
func (c *safeConn) Close() error         { return c.conn.Close() }

type Config struct {
	SignalingURL  string        // e.g. ws://your-vps:9000/ws/host
	Name          string        // display name shown in the viewer's session list
	ICEServers    []string      // STUN/TURN URLs, e.g. "stun:vps:3478"; empty is fine on a LAN/loopback
	ICEUsername   string        // TURN username; ignored by STUN entries
	ICECredential string        // TURN credential; ignored by STUN entries
	JPEGQuality   int           // 1-100; 0 defaults to 70
	FrameDelay    time.Duration // 0 defaults to 100ms (~10fps)
}

func (c *Config) applyDefaults() {
	if c.JPEGQuality <= 0 {
		c.JPEGQuality = 70
	}
	if c.FrameDelay <= 0 {
		c.FrameDelay = 100 * time.Millisecond
	}
}

func (c Config) iceServers() []webrtc.ICEServer {
	if len(c.ICEServers) == 0 {
		return nil
	}
	return []webrtc.ICEServer{{URLs: c.ICEServers, Username: c.ICEUsername, Credential: c.ICECredential}}
}

// reconnectDelay is how long Run waits before retrying after the
// signaling connection fails to establish or drops. A single machine
// running this unattended shouldn't ever need a human to notice a
// transient network hiccup and manually relaunch it.
const reconnectDelay = 5 * time.Second

// Run connects to the signaling server, registers as a host, and for
// every viewer offer it receives, negotiates a WebRTC PeerConnection and
// streams the primary display to it while applying incoming input and
// overlay messages. Unlike a one-shot dial, Run never gives up: if the
// initial connection fails (server briefly unreachable, DNS not ready
// yet, transient network blip) or an established connection drops, it
// logs the error and retries after reconnectDelay, forever — a caller
// like cmd/host's main() that treats a returned error as fatal would
// otherwise kill the whole host process on the very first hiccup,
// requiring a human to notice and relaunch it. Ctrl+C (or killing the
// process) is still the only way to stop it — there is no in-band
// shutdown message.
func Run(cfg Config) error {
	cfg.applyDefaults()

	bounds := &screenBounds{}

	for {
		if err := connectAndServe(cfg, bounds); err != nil {
			log.Printf("hostapp: connection error: %v — reconnecting in %s", err, reconnectDelay)
		}
		time.Sleep(reconnectDelay)
	}
}

// connectAndServe dials the signaling server once, registers as a host,
// and serves incoming messages until the connection fails or drops. Its
// error return is what tells Run's retry loop to try again.
func connectAndServe(cfg Config, bounds *screenBounds) error {
	rawConn, _, err := websocket.DefaultDialer.Dial(cfg.SignalingURL, nil)
	if err != nil {
		return err
	}
	conn := &safeConn{conn: rawConn}
	defer conn.Close()

	payload, _ := json.Marshal(proto.RegisterHost{Name: cfg.Name})
	if err := conn.WriteJSON(proto.Envelope{Type: proto.MsgRegisterHost, Payload: payload}); err != nil {
		return err
	}
	log.Printf("hostapp: registered as %q, waiting for viewers", cfg.Name)

	for {
		var env proto.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return err
		}

		switch env.Type {
		case proto.MsgOffer:
			go handleOffer(conn, cfg, env, bounds)
		case proto.MsgRequestScreenshot:
			go handleScreenshotRequest(conn, env)
		case proto.MsgInputEvent:
			var evt proto.InputEvent
			if err := json.Unmarshal(env.Payload, &evt); err != nil {
				log.Printf("hostapp: bad relayed input event: %v", err)
				continue
			}
			applyInputEvent(evt, bounds)
		case proto.MsgOverlayMessage:
			var msg proto.OverlayMessage
			if err := json.Unmarshal(env.Payload, &msg); err != nil {
				log.Printf("hostapp: bad relayed overlay message: %v", err)
				continue
			}
			showOverlayMessage(msg)
		case proto.MsgStartFrameRelay:
			go runFrameRelayLoop(conn, cfg, bounds, env.SessionID)
		case proto.MsgStopFrameRelay:
			stopFrameRelay(env.SessionID)
		default:
			// MsgICECandidate is relayed by the signaling server but
			// unused here: webrtcconn.Peer waits for full ICE gathering
			// before returning an offer/answer (non-trickle ICE), so the
			// SDP already carries every candidate. Nothing else to do.
		}
	}
}

// handleScreenshotRequest answers a one-off preview request from the
// session list — no WebRTC negotiation, just a single lower-quality
// JPEG sent straight back over the signaling connection.
func handleScreenshotRequest(signaling *safeConn, env proto.Envelope) {
	const previewQuality = 50 // lower than the live video stream's quality; this is just a quick preview
	jpegBytes, _, _, err := capture.GrabPrimaryJPEG(previewQuality)
	if err != nil {
		log.Printf("hostapp: screenshot capture failed: %v", err)
		return
	}
	payload, err := json.Marshal(proto.ScreenshotMessage{JPEG: jpegBytes})
	if err != nil {
		log.Printf("hostapp: failed to encode screenshot: %v", err)
		return
	}
	if err := signaling.WriteJSON(proto.Envelope{Type: proto.MsgScreenshot, SessionID: env.SessionID, Payload: payload}); err != nil {
		log.Printf("hostapp: failed to send screenshot: %v", err)
	}
}

// screenBounds holds the exact pixel dimensions of the most recent
// capture, shared between the capture loop (writer) and incoming
// InputMouseMove handling (reader) for one session. Using the capture's
// own bounds — rather than each side independently querying screen
// resolution — keeps mouse coordinate normalization correct even when
// Windows DPI scaling makes GetSystemMetrics disagree with what GDI
// actually captured.
type screenBounds struct {
	width  atomic.Int32
	height atomic.Int32
}

func handleOffer(signaling *safeConn, cfg Config, env proto.Envelope, bounds *screenBounds) {
	var sdpMsg proto.SDPMessage
	if err := json.Unmarshal(env.Payload, &sdpMsg); err != nil {
		log.Printf("hostapp: bad offer payload for session %s: %v", env.SessionID, err)
		return
	}

	peer, err := webrtcconn.NewPeer(cfg.iceServers())
	if err != nil {
		log.Printf("hostapp: NewPeer failed for session %s: %v", env.SessionID, err)
		return
	}

	answer, err := peer.AcceptOffer(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdpMsg.SDP,
	})
	if err != nil {
		log.Printf("hostapp: AcceptOffer failed for session %s: %v", env.SessionID, err)
		peer.Close()
		return
	}

	answerPayload, _ := json.Marshal(proto.SDPMessage{SDP: answer.SDP})
	if err := signaling.WriteJSON(proto.Envelope{Type: proto.MsgAnswer, SessionID: env.SessionID, Payload: answerPayload}); err != nil {
		log.Printf("hostapp: failed to send answer for session %s: %v", env.SessionID, err)
		peer.Close()
		return
	}

	if err := peer.WaitConnected(15 * time.Second); err != nil {
		log.Printf("hostapp: session %s never connected: %v", env.SessionID, err)
		peer.Close()
		return
	}
	log.Printf("hostapp: session %s connected", env.SessionID)

	peer.OnData(func(data []byte) { handleDataChannelMessage(data, bounds) })

	runCaptureLoop(peer, cfg, bounds)
}

// screenFrameChunkSize keeps each DataChannel message well under every
// WebRTC implementation's message-size limit we know of. A real desktop
// capture routinely exceeds 256KB as a single JPEG, which is why frames
// are always split — see internal/proto.EncodeScreenFrameChunks.
const screenFrameChunkSize = 16 * 1024

// videoBufferedAmountThreshold caps how much unacknowledged video data we
// let pion's DataChannel queue before we start skipping frames instead of
// adding to the backlog. ~4 chunks' worth is enough slack for a brief
// stall without ever letting the picture drift meaningfully behind
// real time.
const videoBufferedAmountThreshold = 4 * screenFrameChunkSize

func runCaptureLoop(peer *webrtcconn.Peer, cfg Config, bounds *screenBounds) {
	defer peer.Close()

	ticker := time.NewTicker(cfg.FrameDelay)
	defer ticker.Stop()

	var seq uint32
	wasDropping := false
	for range ticker.C {
		if buffered := peer.VideoBufferedAmount(); buffered > videoBufferedAmountThreshold {
			if !wasDropping {
				log.Printf("hostapp: video channel congested (buffered=%d bytes), dropping frames until it drains", buffered)
				wasDropping = true
			}
			continue
		}
		if wasDropping {
			log.Printf("hostapp: video channel drained, resuming frame sends")
			wasDropping = false
		}

		jpegBytes, width, height, err := capture.GrabPrimaryJPEG(cfg.JPEGQuality)
		if err != nil {
			log.Printf("hostapp: capture failed: %v", err)
			continue
		}
		bounds.width.Store(width)
		bounds.height.Store(height)
		seq++

		disconnected := false
		for _, chunk := range proto.EncodeScreenFrameChunks(seq, jpegBytes, screenFrameChunkSize) {
			if err := peer.SendVideo(chunk); err != nil {
				// A closed/broken data channel means the viewer
				// disconnected; stop this session's capture loop rather
				// than spinning on send errors forever.
				if strings.Contains(err.Error(), "not established") || isClosedErr(err) {
					log.Printf("hostapp: viewer disconnected, stopping capture loop")
					disconnected = true
					break
				}
				log.Printf("hostapp: send frame chunk failed: %v", err)
				break
			}
		}
		if disconnected {
			return
		}
	}
}

func isClosedErr(err error) bool {
	return strings.Contains(err.Error(), "closed")
}

var (
	frameRelayMu    sync.Mutex
	frameRelayStops = map[string]chan struct{}{}
)

func stopFrameRelay(sessionID string) {
	frameRelayMu.Lock()
	defer frameRelayMu.Unlock()
	if stop, ok := frameRelayStops[sessionID]; ok {
		close(stop)
		delete(frameRelayStops, sessionID)
	}
}

// relayFrameDelay is the HTTP/MJPEG fallback's own capture interval,
// independent of cfg.FrameDelay (the WebRTC path's rate). It's
// deliberately more aggressive than the WebRTC default: this leg sends
// raw binary frames with none of the DataChannel chunking overhead, and
// the drop-stale-frame behavior throughout this path (see the
// WriteBinary/time.Ticker comment below, and FrameBroadcaster.Publish)
// means asking for more than the pipe can sustain just self-throttles
// back down instead of building a backlog — so there's no real downside
// to aiming high here.
const relayFrameDelay = 33 * time.Millisecond // ~30fps target

// runFrameRelayLoop pushes JPEG frames to the signaling server for the
// HTTP/MJPEG fallback, independent of any WebRTC PeerConnection. Unlike
// the WebRTC video path, this doesn't need an explicit buffered-amount
// check: conn.WriteBinary writes straight through to the underlying TCP
// socket, so if the signaling link is backed up the write blocks, and
// time.Ticker already drops ticks that occur while its consumer is busy
// — there is no separate unbounded send queue for this leg to overflow.
func runFrameRelayLoop(conn *safeConn, cfg Config, bounds *screenBounds, sessionID string) {
	// Idempotently stop any pre-existing loop for this session first, so a
	// stray duplicate MsgStartFrameRelay (which shouldn't happen given the
	// signaling server's first/last-subscriber tracking, but isn't
	// guaranteed by anything in this file) can never orphan an old goroutine
	// whose stop channel would otherwise be overwritten in the map below.
	stopFrameRelay(sessionID)

	stop := make(chan struct{})
	frameRelayMu.Lock()
	frameRelayStops[sessionID] = stop
	frameRelayMu.Unlock()
	defer stopFrameRelay(sessionID)

	ticker := time.NewTicker(relayFrameDelay)
	defer ticker.Stop()

	log.Printf("hostapp: starting HTTP frame relay for session %s", sessionID)
	for {
		select {
		case <-stop:
			log.Printf("hostapp: stopping HTTP frame relay for session %s", sessionID)
			return
		case <-ticker.C:
			jpegBytes, width, height, err := capture.GrabPrimaryJPEG(cfg.JPEGQuality)
			if err != nil {
				log.Printf("hostapp: relay capture failed: %v", err)
				continue
			}
			bounds.width.Store(width)
			bounds.height.Store(height)

			if err := conn.WriteBinary(jpegBytes); err != nil {
				log.Printf("hostapp: relay frame send failed: %v", err)
				return
			}
		}
	}
}

func handleDataChannelMessage(data []byte, bounds *screenBounds) {
	msg, err := proto.DecodeFrame(data)
	if err != nil {
		log.Printf("hostapp: bad data channel frame: %v", err)
		return
	}

	switch m := msg.(type) {
	case proto.InputEvent:
		applyInputEvent(m, bounds)
	case proto.OverlayMessage:
		showOverlayMessage(m)
	}
}

// showOverlayMessage validates and displays an overlay message on the
// host's screen, fading it out after the requested duration. Shared by
// both the WebRTC DataChannel path (handleDataChannelMessage) and the
// relayed-over-signaling-WebSocket path (Run's MsgOverlayMessage case).
func showOverlayMessage(m proto.OverlayMessage) {
	if err := m.Validate(); err != nil {
		log.Printf("hostapp: invalid overlay message: %v", err)
		return
	}
	fade := time.Duration(m.FadeSeconds * float64(time.Second))
	go func() {
		if err := overlay.ShowMessage(m.Text, fade, m.Color); err != nil {
			log.Printf("hostapp: overlay.ShowMessage failed: %v", err)
		}
	}()
}

// applyInputEvent maps a proto.InputEvent onto internal/input's Win32
// SendInput wrapper. Only the left mouse button is wired up — the
// wrapper doesn't yet distinguish buttons or support the wheel — which
// matches the MVP's control scope (basic click + move + type).
func applyInputEvent(evt proto.InputEvent, bounds *screenBounds) {
	var err error
	switch evt.Kind {
	case proto.InputMouseMove:
		w, h := bounds.width.Load(), bounds.height.Load()
		if w > 0 && h > 0 {
			err = input.MoveMouse(evt.X, evt.Y, w, h)
		}
	case proto.InputMouseDown:
		err = input.MouseButton(true)
	case proto.InputMouseUp:
		err = input.MouseButton(false)
	case proto.InputKeyDown:
		err = input.KeyPress(evt.KeyVK, true)
	case proto.InputKeyUp:
		err = input.KeyPress(evt.KeyVK, false)
	case proto.InputMouseWheel:
		// not implemented in internal/input for the MVP; silently ignored.
	}
	if err != nil {
		log.Printf("hostapp: input injection failed for %s: %v", evt.Kind, err)
	}
}
