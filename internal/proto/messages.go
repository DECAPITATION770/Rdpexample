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
	InputMouseMove  InputEventKind = "mouse_move"
	InputMouseDown  InputEventKind = "mouse_down"
	InputMouseUp    InputEventKind = "mouse_up"
	InputMouseWheel InputEventKind = "mouse_wheel"
	InputKeyDown    InputEventKind = "key_down"
	InputKeyUp      InputEventKind = "key_up"
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
