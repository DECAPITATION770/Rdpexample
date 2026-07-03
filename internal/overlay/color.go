package overlay

import (
	"strconv"
	"strings"
)

// defaultTextColor is plain white — used whenever a message doesn't
// specify a color, or specifies one ParseHexColor can't understand, so
// the overlay stays visible instead of silently failing to show
// anything.
var defaultTextColor = [3]byte{255, 255, 255}

// ParseHexColor parses a "#RRGGBB" (or "RRGGBB", case-insensitive)
// string into RGB bytes. Empty or malformed input returns the default
// white and ok=false, so callers can choose to log the fallback or just
// use it silently.
func ParseHexColor(s string) (r, g, b byte, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return defaultTextColor[0], defaultTextColor[1], defaultTextColor[2], false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return defaultTextColor[0], defaultTextColor[1], defaultTextColor[2], false
	}
	return byte(v >> 16), byte(v >> 8), byte(v), true
}
