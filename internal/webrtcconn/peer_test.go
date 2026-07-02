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
