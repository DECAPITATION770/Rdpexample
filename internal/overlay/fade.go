package overlay

import "time"

// FadeTimer computes a linear opacity ramp from 1.0 down to 0.0 over a
// fixed total duration. The renderer (Windows-only) polls
// Opacity(elapsed) on a ticker and calls SetLayeredWindowAttributes with
// the result; IsExpired tells it when to destroy the window.
type FadeTimer struct {
	total time.Duration
}

func NewFadeTimer(total time.Duration) *FadeTimer {
	return &FadeTimer{total: total}
}

func (f *FadeTimer) Opacity(elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 1.0
	}
	if elapsed >= f.total {
		return 0.0
	}
	return 1.0 - float64(elapsed)/float64(f.total)
}

func (f *FadeTimer) IsExpired(elapsed time.Duration) bool {
	return elapsed >= f.total
}
