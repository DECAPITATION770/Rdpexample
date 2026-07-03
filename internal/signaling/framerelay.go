package signaling

import "sync"

// FrameBroadcaster fans out JPEG frames for one session to any number of
// HTTP subscribers. Publish never blocks: a subscriber whose channel
// still holds the previous frame simply misses the new one — the same
// "never queue a stale frame" rule the WebRTC video path enforces via
// BufferedAmount (see internal/webrtcconn.Peer.VideoBufferedAmount).
type FrameBroadcaster struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func NewFrameBroadcaster() *FrameBroadcaster {
	return &FrameBroadcaster{subs: make(map[chan []byte]struct{})}
}

// Subscribe registers a new frame receiver. cancel must be called when
// the subscriber is done (e.g. the HTTP request ends) to free it.
func (b *FrameBroadcaster) Subscribe() (ch chan []byte, cancel func()) {
	var trackedCancel func() bool
	ch, trackedCancel, _ = b.SubscribeTracked()
	cancel = func() { trackedCancel() }
	return ch, cancel
}

// SubscribeTracked is Subscribe plus bookkeeping the HTTP handler needs
// to tell the host when to start/stop relaying: isFirst reports whether
// this was the only subscriber at the moment it joined, and the returned
// cancel function reports whether it was the last one to leave.
func (b *FrameBroadcaster) SubscribeTracked() (ch chan []byte, cancel func() (isLast bool), isFirst bool) {
	ch = make(chan []byte, 1)
	b.mu.Lock()
	isFirst = len(b.subs) == 0
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	cancel = func() bool {
		b.mu.Lock()
		delete(b.subs, ch)
		isLast := len(b.subs) == 0
		b.mu.Unlock()
		return isLast
	}
	return ch, cancel, isFirst
}

// Publish fans jpeg out to every current subscriber without blocking.
func (b *FrameBroadcaster) Publish(jpeg []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- jpeg:
		default: // subscriber hasn't drained the previous frame; drop this one
		}
	}
}
