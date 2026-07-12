//go:build linux

package capture

import (
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
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
