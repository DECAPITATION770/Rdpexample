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
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"rdpAiAnswer/internal/capture"
	"rdpAiAnswer/internal/input"
	"rdpAiAnswer/internal/overlay"
	"rdpAiAnswer/internal/proto"
	"rdpAiAnswer/internal/webrtcconn"
)

type Config struct {
	SignalingURL string        // e.g. ws://your-vps:9000/ws/host
	Name         string        // display name shown in the viewer's session list
	ICEServers   []string      // STUN/TURN URLs, e.g. "stun:vps:3478"; empty is fine on a LAN/loopback
	JPEGQuality  int           // 1-100; 0 defaults to 70
	FrameDelay   time.Duration // 0 defaults to 100ms (~10fps)
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
	return []webrtc.ICEServer{{URLs: c.ICEServers}}
}

// Run connects to the signaling server, registers as a host, and for
// every viewer offer it receives, negotiates a WebRTC PeerConnection and
// streams the primary display to it while applying incoming input and
// overlay messages. It blocks until the signaling connection drops or
// fails; callers that want graceful shutdown should run it in a
// goroutine and close the process via other means (Ctrl+C is enough for
// the MVP — there is no in-band shutdown message).
func Run(cfg Config) error {
	cfg.applyDefaults()

	conn, _, err := websocket.DefaultDialer.Dial(cfg.SignalingURL, nil)
	if err != nil {
		return err
	}
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
			go handleOffer(conn, cfg, env)
		default:
			// MsgICECandidate is relayed by the signaling server but
			// unused here: webrtcconn.Peer waits for full ICE gathering
			// before returning an offer/answer (non-trickle ICE), so the
			// SDP already carries every candidate. Nothing else to do.
		}
	}
}

func handleOffer(signaling *websocket.Conn, cfg Config, env proto.Envelope) {
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

	peer.OnData(func(data []byte) { handleDataChannelMessage(data) })

	runCaptureLoop(peer, cfg)
}

func runCaptureLoop(peer *webrtcconn.Peer, cfg Config) {
	defer peer.Close()

	ticker := time.NewTicker(cfg.FrameDelay)
	defer ticker.Stop()

	var seq uint32
	for range ticker.C {
		jpegBytes, err := capture.GrabPrimaryJPEG(cfg.JPEGQuality)
		if err != nil {
			log.Printf("hostapp: capture failed: %v", err)
			continue
		}
		seq++
		frame, err := proto.EncodeFrame(proto.ScreenFrame{JPEG: jpegBytes, Seq: seq})
		if err != nil {
			log.Printf("hostapp: encode frame failed: %v", err)
			continue
		}
		if err := peer.Send(frame); err != nil {
			// A closed/broken data channel means the viewer disconnected;
			// stop this session's capture loop rather than spinning on
			// send errors forever.
			if strings.Contains(err.Error(), "not established") || isClosedErr(err) {
				log.Printf("hostapp: viewer disconnected, stopping capture loop")
				return
			}
			log.Printf("hostapp: send frame failed: %v", err)
		}
	}
}

func isClosedErr(err error) bool {
	return strings.Contains(err.Error(), "closed")
}

func handleDataChannelMessage(data []byte) {
	msg, err := proto.DecodeFrame(data)
	if err != nil {
		log.Printf("hostapp: bad data channel frame: %v", err)
		return
	}

	switch m := msg.(type) {
	case proto.InputEvent:
		applyInputEvent(m)
	case proto.OverlayMessage:
		if err := m.Validate(); err != nil {
			log.Printf("hostapp: invalid overlay message: %v", err)
			return
		}
		fade := time.Duration(m.FadeSeconds * float64(time.Second))
		go func() {
			if err := overlay.ShowMessage(m.Text, fade); err != nil {
				log.Printf("hostapp: overlay.ShowMessage failed: %v", err)
			}
		}()
	}
}

// applyInputEvent maps a proto.InputEvent onto internal/input's Win32
// SendInput wrapper. Only the left mouse button is wired up — the
// wrapper doesn't yet distinguish buttons or support the wheel — which
// matches the MVP's control scope (basic click + move + type).
func applyInputEvent(evt proto.InputEvent) {
	var err error
	switch evt.Kind {
	case proto.InputMouseMove:
		err = input.MoveMouse(evt.X, evt.Y)
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
