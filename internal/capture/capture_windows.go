//go:build windows

package capture

import (
	"bytes"
	"image/jpeg"

	"github.com/kbinani/screenshot"
)

// GrabPrimaryJPEG captures the primary display and returns it
// JPEG-encoded at the given quality (1-100), along with the exact pixel
// dimensions that were captured. Callers should feed width/height back
// into internal/input.MoveMouse for coordinate normalization rather than
// querying GetSystemMetrics separately — on a display with Windows DPI
// scaling active, GetSystemMetrics can report a different (logical)
// resolution than what GDI actually captures (physical pixels), which
// silently throws off absolute mouse positioning. Using the capture's
// own bounds as the single source of truth keeps both sides consistent
// no matter what DPI scaling is in effect.
func GrabPrimaryJPEG(quality int) (jpegBytes []byte, width, height int32, err error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, 0, 0, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), int32(bounds.Dx()), int32(bounds.Dy()), nil
}
