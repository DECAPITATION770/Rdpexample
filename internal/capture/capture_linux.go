//go:build linux

package capture

import (
	"bytes"
	"image/jpeg"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/kbinani/screenshot"
)

// x11 holds one lazily-opened connection to the X server (from $DISPLAY)
// for the process's lifetime, used only to query the current cursor
// position for the marker drawn below. It's deliberately separate from
// whatever connection kbinani/screenshot opens internally for the
// actual capture — this package doesn't control that library's
// connection lifecycle, so it manages its own.
var (
	x11Once sync.Once
	x11Conn *xgb.Conn
	x11Root xproto.Window
)

func connectX11() {
	conn, err := xgb.NewConn()
	if err != nil {
		return
	}
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 {
		conn.Close()
		return
	}
	x11Conn = conn
	x11Root = setup.DefaultScreen(conn).Root
}

// cursorScreenPos returns the current mouse position in root-window
// (screen) coordinates, the same space GDI's capture already uses on
// Windows. A connection failure (no DISPLAY, no reachable X server)
// just means captures won't have a cursor marker drawn on them — not
// fatal to screen capture itself, so this only ever reports ok=false
// rather than an error.
func cursorScreenPos() (x, y int32, ok bool) {
	x11Once.Do(connectX11)
	if x11Conn == nil {
		return 0, 0, false
	}
	reply, err := xproto.QueryPointer(x11Conn, x11Root).Reply()
	if err != nil || reply == nil {
		return 0, 0, false
	}
	return int32(reply.RootX), int32(reply.RootY), true
}

// GrabPrimaryJPEG captures the primary display and returns it
// JPEG-encoded at the given quality (1-100), along with the exact pixel
// dimensions that were captured — the same contract as the Windows
// implementation (capture_windows.go), so internal/hostapp doesn't need
// to know which platform it's running on. kbinani/screenshot already
// supports X11 (via this same jezek/xgb + shm), so the actual capture
// call is identical to the Windows version; only cursor-position lookup
// differs per platform.
func GrabPrimaryJPEG(quality int) (jpegBytes []byte, width, height int32, err error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, 0, 0, err
	}
	if x, y, ok := cursorScreenPos(); ok {
		drawCursorMarkerAt(img, bounds.Min.X, bounds.Min.Y, x, y)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), int32(bounds.Dx()), int32(bounds.Dy()), nil
}
