//go:build windows

package capture

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"unsafe"

	"github.com/kbinani/screenshot"
	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procGetCursorPos = user32.NewProc("GetCursorPos")
)

type cursorPoint struct{ X, Y int32 }

// cursorScreenPos returns the current mouse position in the same Win32
// screen-coordinate space GDI's capture already uses.
func cursorScreenPos() (x, y int32, ok bool) {
	var p cursorPoint
	ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p.X, p.Y, ret != 0
}

// drawCursorMarker draws a simple, theme-independent pointer marker
// (filled white dot, black outline) onto img at the current cursor
// position. GDI's BitBlt-based capture (via kbinani/screenshot) never
// includes the system cursor — it's composited by the OS on top of the
// framebuffer, not part of what BitBlt copies — so without this, a
// viewer has no way to tell where the mouse actually is. This sidesteps
// the much more involved (and cursor-theme/DPI-fragile) alternative of
// extracting and alpha-blending the real cursor icon bitmap via
// GetIconInfo/GetDIBits; a plain marker is enough to answer "where is
// the pointer" even though it won't match the OS cursor's shape.
func drawCursorMarker(img *image.RGBA, boundsMinX, boundsMinY int) {
	x, y, ok := cursorScreenPos()
	if !ok {
		return
	}
	cx := int(x) - boundsMinX
	cy := int(y) - boundsMinY

	const radius = 6
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	imgBounds := img.Bounds()
	for dy := -radius - 1; dy <= radius+1; dy++ {
		for dx := -radius - 1; dx <= radius+1; dx++ {
			px, py := cx+dx, cy+dy
			if px < imgBounds.Min.X || py < imgBounds.Min.Y || px >= imgBounds.Max.X || py >= imgBounds.Max.Y {
				continue
			}
			distSq := dx*dx + dy*dy
			switch {
			case distSq <= radius*radius:
				img.Set(px, py, white)
			case distSq <= (radius+1)*(radius+1):
				img.Set(px, py, black)
			}
		}
	}
}

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
	drawCursorMarker(img, bounds.Min.X, bounds.Min.Y)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), int32(bounds.Dx()), int32(bounds.Dy()), nil
}
