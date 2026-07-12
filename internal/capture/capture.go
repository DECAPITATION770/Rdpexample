//go:build windows || linux

package capture

import (
	"github.com/kbinani/screenshot"
)

// ForceEncode is the prevHash value that always encodes: no real frame
// hash can equal it, since frameHash only ever fills the low 32 bits
// (it's a CRC32), so the high bits being set makes it unreachable. Pass
// it (or just a fresh loop's initial value) to force a first frame out
// even when the screen is, say, all black — whose genuine hash is 0 and
// would otherwise be mistaken for "unchanged since a zero-initialized
// prevHash."
const ForceEncode = ^uint64(0)

// GrabPrimaryFrame captures the primary display, hashes it, and — only if
// that hash differs from prevHash — JPEG-encodes it at the given quality
// (1-100), optionally downscaled so its width is at most maxWidth (0 =
// native resolution, no scaling).
//
// When the screen is unchanged (hash == prevHash) it returns
// jpegBytes == nil and skips the encode entirely: that skipped encode is
// the whole point, since re-JPEG-ing a static 1080p screen costs tens of
// milliseconds. Callers should treat a nil jpegBytes (with nil err) as
// "nothing to send this tick" and keep using the previous frame's
// dimensions. The returned hash should be fed back as the next call's
// prevHash.
//
// When it does encode, width/height are the dimensions actually encoded —
// which differ from the native screen size when downscaling is active,
// and which callers must feed back into internal/input.MoveMouse for
// correct coordinate normalization.
//
// The per-OS split is only in cursorScreenPos (capture_windows.go /
// capture_linux.go); everything else here is identical across platforms,
// which is why it lives in this shared file rather than being duplicated.
func GrabPrimaryFrame(quality, maxWidth int, prevHash uint64) (jpegBytes []byte, width, height int32, hash uint64, err error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	if x, y, ok := cursorScreenPos(); ok {
		drawCursorMarkerAt(img, bounds.Min.X, bounds.Min.Y, x, y)
	}
	// Hash the composited image (cursor marker included), so a pure cursor
	// move still counts as a changed frame, then bail before the expensive
	// encode if nothing changed.
	h := frameHash(img)
	if h == prevHash {
		return nil, 0, 0, h, nil
	}
	jpegBytes, width, height, err = encodeJPEG(img, quality, maxWidth)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return jpegBytes, width, height, h, nil
}

// GrabPrimaryJPEG is the simpler capture used for one-off previews (the
// session-list screenshot) and the test commands: native resolution,
// always encodes (no frame-skip). It's a thin wrapper over
// GrabPrimaryFrame so both paths share the exact same capture/encode code.
func GrabPrimaryJPEG(quality int) (jpegBytes []byte, width, height int32, err error) {
	jpegBytes, width, height, _, err = GrabPrimaryFrame(quality, 0, ForceEncode)
	return jpegBytes, width, height, err
}
