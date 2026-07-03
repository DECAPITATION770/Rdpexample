//go:build windows

package capture

import (
	"bytes"
	"image/jpeg"

	"github.com/kbinani/screenshot"
)

// GrabPrimaryJPEG captures the primary display and returns it JPEG-encoded
// at the given quality (1-100). Called once per frame by the host's
// capture loop.
func GrabPrimaryJPEG(quality int) ([]byte, error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
