package webrtcconn

import (
	"errors"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// Peer wraps a pion PeerConnection plus a single named DataChannel
// ("control") used for both directions of proto-framed messages
// (screen frames, input events, overlay messages — see internal/proto).
// Both Host and Viewer use this same type; whichever side calls
// CreateOffer first is the offerer.
type Peer struct {
	pc     *webrtc.PeerConnection
	dc     *webrtc.DataChannel
	onData func([]byte)

	dcOpenOnce sync.Once
	dcOpen     chan struct{}
}

// NewPeer creates a Peer. iceServers of nil means host-candidates-only
// (fine for same-machine tests; production callers pass STUN/TURN URLs
// from config).
func NewPeer(iceServers []webrtc.ICEServer) (*Peer, error) {
	config := webrtc.Configuration{ICEServers: iceServers}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	p := &Peer{pc: pc, dcOpen: make(chan struct{})}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		p.attachDataChannel(dc)
	})

	return p, nil
}

func (p *Peer) attachDataChannel(dc *webrtc.DataChannel) {
	p.dc = dc
	dc.OnOpen(func() {
		p.dcOpenOnce.Do(func() { close(p.dcOpen) })
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if p.onData != nil {
			p.onData(msg.Data)
		}
	})
}

func (p *Peer) OnData(fn func([]byte)) { p.onData = fn }

func (p *Peer) CreateOffer() (webrtc.SessionDescription, error) {
	dc, err := p.pc.CreateDataChannel("control", nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	p.attachDataChannel(dc)

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

// WaitConnected blocks until this Peer's DataChannel is open and ready to
// Send on. Peer connection state can report "connected" slightly before
// the DataChannel's own SCTP stream finishes opening, so this waits on
// the DataChannel directly rather than on PeerConnectionState.
func (p *Peer) WaitConnected(timeout time.Duration) error {
	select {
	case <-p.dcOpen:
		return nil
	case <-time.After(timeout):
		return errors.New("webrtcconn: timed out waiting for data channel to open")
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
