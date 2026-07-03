package webrtcconn

import (
	"errors"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	channelControl = "control" // input events + overlay messages: tiny, must arrive in order
	channelVideo   = "video"   // screen frame chunks: large and frequent — must never queue behind, or block, control traffic
)

// Peer wraps a pion PeerConnection with two DataChannels: "control"
// (ordered/reliable — InputEvent and OverlayMessage, see internal/proto)
// and "video" (unordered, zero-retransmit — screen frame chunks, see
// internal/proto.EncodeScreenFrameChunks). Putting video on its own
// unreliable channel matters: an ordered channel delivers messages
// strictly in send order, so a multi-chunk video frame in flight would
// otherwise force every subsequent mouse-move/keypress to wait behind
// it — exactly the kind of head-of-line blocking that makes remote
// control feel laggy. Video frames are also fine to just drop when lost
// (the next frame supersedes it), so retransmission isn't worth the
// latency it would add either.
//
// Both Host and Viewer use this same type; whichever side calls
// CreateOffer first is the offerer — the offerer is the one that must
// call CreateDataChannel, since WebRTC delivers channels created by one
// side to the other automatically via OnDataChannel.
type Peer struct {
	pc        *webrtc.PeerConnection
	dcControl *webrtc.DataChannel
	dcVideo   *webrtc.DataChannel
	onData    func([]byte)

	openMu     sync.Mutex
	opened     map[string]bool
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
	p := &Peer{pc: pc, dcOpen: make(chan struct{}), opened: make(map[string]bool)}

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		p.attachDataChannel(dc)
	})

	return p, nil
}

func (p *Peer) attachDataChannel(dc *webrtc.DataChannel) {
	switch dc.Label() {
	case channelVideo:
		p.dcVideo = dc
	default:
		p.dcControl = dc
	}

	label := dc.Label()
	dc.OnOpen(func() { p.markOpen(label) })
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if p.onData != nil {
			p.onData(msg.Data)
		}
	})
}

func (p *Peer) markOpen(label string) {
	p.openMu.Lock()
	p.opened[label] = true
	ready := p.opened[channelControl] && p.opened[channelVideo]
	p.openMu.Unlock()
	if ready {
		p.dcOpenOnce.Do(func() { close(p.dcOpen) })
	}
}

func (p *Peer) OnData(fn func([]byte)) { p.onData = fn }

func (p *Peer) CreateOffer() (webrtc.SessionDescription, error) {
	control, err := p.pc.CreateDataChannel(channelControl, nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	p.attachDataChannel(control)

	ordered := false
	maxRetransmits := uint16(0)
	video, err := p.pc.CreateDataChannel(channelVideo, &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
	})
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	p.attachDataChannel(video)

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

// WaitConnected blocks until both DataChannels are open and ready to
// Send on. Peer connection state can report "connected" slightly before
// a DataChannel's own SCTP stream finishes opening, so this waits on the
// channels directly rather than on PeerConnectionState.
func (p *Peer) WaitConnected(timeout time.Duration) error {
	select {
	case <-p.dcOpen:
		return nil
	case <-time.After(timeout):
		return errors.New("webrtcconn: timed out waiting for data channels to open")
	}
}

// SendControl sends on the ordered/reliable channel — input events and
// overlay messages.
func (p *Peer) SendControl(data []byte) error {
	if p.dcControl == nil {
		return errors.New("webrtcconn: control data channel not established")
	}
	return p.dcControl.Send(data)
}

// SendVideo sends on the unordered/unreliable channel — screen frame
// chunks. A failed/lost chunk just means that frame gets skipped; the
// next one supersedes it.
func (p *Peer) SendVideo(data []byte) error {
	if p.dcVideo == nil {
		return errors.New("webrtcconn: video data channel not established")
	}
	return p.dcVideo.Send(data)
}

func (p *Peer) Close() error {
	return p.pc.Close()
}
