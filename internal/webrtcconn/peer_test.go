package webrtcconn

import (
	"testing"
	"time"
)

func TestPeer_ControlChannelRoundTrip(t *testing.T) {
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

	if err := offerer.SendControl([]byte("hello")); err != nil {
		t.Fatalf("SendControl: %v", err)
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

func TestPeer_VideoChannelRoundTrip(t *testing.T) {
	offerer, err := NewPeer(nil)
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

	if err := offerer.SendVideo([]byte("frame-chunk")); err != nil {
		t.Fatalf("SendVideo: %v", err)
	}

	select {
	case data := <-received:
		if string(data) != "frame-chunk" {
			t.Fatalf("received %q, want %q", data, "frame-chunk")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for data channel message")
	}
}

func TestPeer_SendBeforeConnectedFails(t *testing.T) {
	p, err := NewPeer(nil)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

	if err := p.SendControl([]byte("x")); err == nil {
		t.Fatal("SendControl before any offer/answer exchange should fail, got nil error")
	}
	if err := p.SendVideo([]byte("x")); err == nil {
		t.Fatal("SendVideo before any offer/answer exchange should fail, got nil error")
	}
}

func TestPeer_VideoBufferedAmount_ZeroBeforeConnected(t *testing.T) {
	p, err := NewPeer(nil)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

	if got := p.VideoBufferedAmount(); got != 0 {
		t.Fatalf("VideoBufferedAmount() before any data channel exists = %d, want 0", got)
	}
}

func TestPeer_VideoBufferedAmount_TracksAfterConnect(t *testing.T) {
	offerer, err := NewPeer(nil)
	if err != nil {
		t.Fatalf("NewPeer offerer: %v", err)
	}
	defer offerer.Close()
	answerer, err := NewPeer(nil)
	if err != nil {
		t.Fatalf("NewPeer answerer: %v", err)
	}
	defer answerer.Close()

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

	// Connected with nothing sent yet: buffered amount settles to 0. This
	// just proves the method reads the real channel post-connect rather
	// than always returning the pre-connect zero value from a nil
	// dcVideo. Immediately after WaitConnected returns, pion's DCEP
	// handshake control message may not have fully flushed from the SCTP
	// send buffer yet, so VideoBufferedAmount() can transiently report a
	// small nonzero value even though the channel is open. Poll for it
	// to settle rather than asserting on a specific instant.
	deadline := time.Now().Add(500 * time.Millisecond)
	var got uint64
	for {
		got = offerer.VideoBufferedAmount()
		if got == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got != 0 {
		t.Fatalf("VideoBufferedAmount() after connect (waited up to 500ms to settle) = %d, want 0", got)
	}
}
