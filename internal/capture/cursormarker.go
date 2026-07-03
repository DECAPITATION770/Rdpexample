package capture

import (
	"image"
	"image/color"
)

// drawCursorMarkerAt draws a simple, theme-independent pointer marker
// (filled white dot, black outline) onto img at screen position (x, y),
// offset by the capture's own origin (a multi-monitor capture might not
// start at (0,0)). Neither platform's screen-capture API includes the
// system cursor — it's composited by the OS on top of the framebuffer,
// not part of what the capture copies — so without this, a viewer has
// no way to tell where the mouse actually is. Shared between the
// per-OS cursor-position lookups (capture_windows.go, capture_linux.go)
// since the actual pixel-drawing is identical either way; a plain
// marker sidesteps the much more involved (and cursor-theme/DPI
// fragile) alternative of extracting and blending the real cursor icon.
func drawCursorMarkerAt(img *image.RGBA, boundsMinX, boundsMinY int, x, y int32) {
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
