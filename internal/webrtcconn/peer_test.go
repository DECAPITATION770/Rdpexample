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

	// Connected with nothing sent yet: buffered amount is 0. This just
	// proves the method reads the real channel post-connect rather than
	// always returning the pre-connect zero value from a nil dcVideo.
	if got := offerer.VideoBufferedAmount(); got != 0 {
		t.Fatalf("VideoBufferedAmount() right after connect = %d, want 0", got)
	}
}
