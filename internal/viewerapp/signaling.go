package viewerapp

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"rdpAiAnswer/internal/proto"
)

// signalingClient owns the single WebSocket connection a Viewer process
// keeps open to the signaling server. gorilla/websocket forbids
// concurrent reads (and separately, concurrent writes) on one Conn from
// multiple goroutines, so a single reader goroutine here dispatches
// incoming envelopes to whichever destination wants them: the session
// list screen (list responses) or the currently open control window
// (answer/ICE replies for its session).
type signalingClient struct {
	conn *websocket.Conn

	writeMu sync.Mutex

	sessionListC chan proto.SessionList
	sdpC         chan proto.Envelope // answer + ice_candidate messages for the active control window
}

func newSignalingClient(baseURL string) (*signalingClient, error) {
	conn, _, err := websocket.DefaultDialer.Dial(baseURL+"/ws/viewer", nil)
	if err != nil {
		return nil, err
	}
	c := &signalingClient{
		conn:         conn,
		sessionListC: make(chan proto.SessionList, 1),
		sdpC:         make(chan proto.Envelope, 8),
	}
	go c.readLoop()
	return c, nil
}

func (c *signalingClient) readLoop() {
	defer close(c.sessionListC)
	defer close(c.sdpC)

	for {
		var env proto.Envelope
		if err := c.conn.ReadJSON(&env); err != nil {
			return
		}
		switch env.Type {
		case proto.MsgSessionList:
			var list proto.SessionList
			if err := json.Unmarshal(env.Payload, &list); err != nil {
				continue
			}
			select {
			case c.sessionListC <- list:
			default:
				// Drop a stale list if nobody's currently waiting; the
				// next explicit refresh request will get a fresh one.
			}
		case proto.MsgAnswer, proto.MsgICECandidate:
			c.sdpC <- env
		}
	}
}

func (c *signalingClient) write(env proto.Envelope) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(env)
}

// requestSessionList sends the list request and blocks (up to timeout)
// for the response. This runs on Fyne's UI goroutine when called from a
// button handler — acceptable for a same-VPS/LAN round trip in the MVP,
// but a real progress indicator would make this async if latency grows.
func (c *signalingClient) requestSessionList(timeout time.Duration) ([]proto.SessionInfo, error) {
	if err := c.write(proto.Envelope{Type: proto.MsgListSessions}); err != nil {
		return nil, err
	}
	select {
	case list, ok := <-c.sessionListC:
		if !ok {
			return nil, errors.New("viewerapp: signaling connection closed")
		}
		return list.Sessions, nil
	case <-time.After(timeout):
		return nil, errors.New("viewerapp: timed out waiting for session list")
	}
}

func (c *signalingClient) sendOffer(sessionID, sdp string) error {
	payload, err := json.Marshal(proto.SDPMessage{SDP: sdp})
	if err != nil {
		return err
	}
	return c.write(proto.Envelope{Type: proto.MsgOffer, SessionID: sessionID, Payload: payload})
}

// waitAnswer blocks for the next answer/ICE envelope for the active
// control window's session. Task 13's control window is the only reader
// of sdpC at a time, matching the MVP's one-session-at-a-time UX.
func (c *signalingClient) waitAnswer(timeout time.Duration) (proto.Envelope, error) {
	select {
	case env, ok := <-c.sdpC:
		if !ok {
			return proto.Envelope{}, errors.New("viewerapp: signaling connection closed")
		}
		return env, nil
	case <-time.After(timeout):
		return proto.Envelope{}, errors.New("viewerapp: timed out waiting for answer")
	}
}
