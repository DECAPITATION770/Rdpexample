package overlay

import (
	"testing"
	"time"
)

func TestFadeTimer_OpacityAtStart(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if got := f.Opacity(0); got != 1.0 {
		t.Fatalf("Opacity(0) = %v, want 1.0", got)
	}
}

func TestFadeTimer_OpacityAtEnd(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if got := f.Opacity(2 * time.Second); got != 0.0 {
		t.Fatalf("Opacity(total) = %v, want 0.0", got)
	}
}

func TestFadeTimer_OpacityHalfway(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if got := f.Opacity(1 * time.Second); got != 0.5 {
		t.Fatalf("Opacity(half) = %v, want 0.5", got)
	}
}

func TestFadeTimer_OpacityClampsPastEnd(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if got := f.Opacity(10 * time.Second); got != 0.0 {
		t.Fatalf("Opacity(past end) = %v, want 0.0", got)
	}
}

func TestFadeTimer_IsExpired(t *testing.T) {
	f := NewFadeTimer(2 * time.Second)
	if f.IsExpired(1900 * time.Millisecond) {
		t.Fatal("should not be expired just before total duration")
	}
	if !f.IsExpired(2 * time.Second) {
		t.Fatal("should be expired at total duration")
	}
}
