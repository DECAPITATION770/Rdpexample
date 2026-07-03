package signaling

import "testing"

func TestFrameBroadcaster_DeliversPublishedFrame(t *testing.T) {
	b := NewFrameBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()

	b.Publish([]byte("frame-1"))

	select {
	case got := <-ch:
		if string(got) != "frame-1" {
			t.Fatalf("got %q, want %q", got, "frame-1")
		}
	default:
		t.Fatal("expected a frame to be immediately available")
	}
}

func TestFrameBroadcaster_DropsStaleFrameForSlowSubscriber(t *testing.T) {
	b := NewFrameBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()

	// Publish twice without draining the channel — the second publish
	// must not block, and the subscriber should end up seeing only the
	// latest frame, never a backlog of both.
	b.Publish([]byte("frame-1"))
	b.Publish([]byte("frame-2"))

	got := <-ch
	if string(got) != "frame-1" {
		t.Fatalf("got %q, want %q (the first frame, since it arrived before frame-2 was dropped)", got, "frame-1")
	}
	select {
	case extra := <-ch:
		t.Fatalf("expected channel empty after draining one frame, got extra: %q", extra)
	default:
	}
}

func TestFrameBroadcaster_FansOutToMultipleSubscribers(t *testing.T) {
	b := NewFrameBroadcaster()
	ch1, cancel1 := b.Subscribe()
	defer cancel1()
	ch2, cancel2 := b.Subscribe()
	defer cancel2()

	b.Publish([]byte("frame"))

	for i, ch := range []chan []byte{ch1, ch2} {
		select {
		case got := <-ch:
			if string(got) != "frame" {
				t.Fatalf("subscriber %d got %q, want %q", i, got, "frame")
			}
		default:
			t.Fatalf("subscriber %d: expected a frame to be immediately available", i)
		}
	}
}

func TestFrameBroadcaster_SubscribeReportsFirstAndCancelReportsLast(t *testing.T) {
	b := NewFrameBroadcaster()

	_, cancel1, isFirst1 := b.SubscribeTracked()
	if !isFirst1 {
		t.Fatal("first Subscribe should report isFirst=true")
	}
	_, cancel2, isFirst2 := b.SubscribeTracked()
	if isFirst2 {
		t.Fatal("second Subscribe should report isFirst=false")
	}

	if isLast := cancel1(); isLast {
		t.Fatal("cancelling one of two subscribers should report isLast=false")
	}
	if isLast := cancel2(); !isLast {
		t.Fatal("cancelling the last subscriber should report isLast=true")
	}
}
