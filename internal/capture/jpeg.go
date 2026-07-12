// Pure image helpers with no platform or capture dependencies, so they
// build and unit-test on any OS (including the dev machine) even though
// the actual screen grab in capture.go only builds for windows/linux.
package capture

import (
	"bytes"
	"hash/crc32"
	"image"
	"image/jpeg"

	xdraw "golang.org/x/image/draw"
)

// castagnoli uses the CRC32-C polynomial, which is hardware-accelerated
// on amd64/arm64 — hashing a full 1080p frame's ~8MB of pixels costs a
// couple of milliseconds, cheap enough to run every frame to decide
// whether the screen actually changed (see frameHash's use in the host's
// capture loops) and skip the far more expensive JPEG encode + network
// send when it hasn't.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// frameHash returns a fast content hash of the image's pixels. Two
// captures of an unchanged screen hash equal, so the caller can skip
// re-encoding and re-sending an identical frame. It hashes img.Pix
// directly (the raw RGBA bytes) rather than anything semantic — any
// visible change, including the cursor marker having moved, flips the
// hash, which is exactly the granularity the frame-skip wants.
func frameHash(img *image.RGBA) uint64 {
	return uint64(crc32.Checksum(img.Pix, castagnoli))
}

// downscale returns img shrunk so its width is at most maxWidth, keeping
// the aspect ratio. maxWidth <= 0, or an image already no wider than
// maxWidth, is returned unchanged (no copy, no quality loss) — this is
// the default path, so capturing at native resolution costs nothing
// extra. When it does scale, it uses approximate bilinear sampling:
// meaningfully smoother on text and thin lines than nearest-neighbor
// (which aliases badly — the visible "quality loss" people fear from
// downscaling), while far cheaper than a high-order kernel.
func downscale(img *image.RGBA, maxWidth int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if maxWidth <= 0 || w <= maxWidth {
		return img
	}
	newW := maxWidth
	newH := h * maxWidth / w
	if newH < 1 {
		newH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

// encodeJPEG optionally downscales img to maxWidth (0 = native), then
// JPEG-encodes it at the given quality, returning the bytes and the
// dimensions actually encoded (post-downscale). Those dimensions are
// what the caller must use for mouse-coordinate normalization, since a
// downscaled frame no longer matches the native screen size.
func encodeJPEG(img *image.RGBA, quality, maxWidth int) (jpegBytes []byte, width, height int32, err error) {
	scaled := downscale(img, maxWidth)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	sb := scaled.Bounds()
	return buf.Bytes(), int32(sb.Dx()), int32(sb.Dy()), nil
}
