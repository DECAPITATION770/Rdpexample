//go:build linux

// Input injection on X11 via the XTEST extension (no cgo — jezek/xgb is
// a pure-Go reimplementation of the X11 wire protocol, same dependency
// kbinani/screenshot already pulls in for its Linux capture backend).
//
// UNTESTED ON REAL HARDWARE: everything here is written against the
// documented XTEST protocol and jezek/xgb's API, but there is no X11
// display available in the environment this was developed in — only
// cross-compilation was possible. Verify on a real X11 session before
// trusting it.
package input

import (
	"errors"
	"fmt"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

var (
	connOnce sync.Once
	connErr  error
	conn     *xgb.Conn
	root     xproto.Window

	// keysymToKeycode is built once from the X server's current keyboard
	// mapping, so key injection works whatever physical layout is
	// actually configured on the target machine rather than assuming a
	// fixed US layout.
	keysymToKeycode map[xproto.Keysym]xproto.Keycode
)

// vkToKeysym maps the same Windows virtual-key codes admin.html's
// CODE_TO_VK table can produce to their standard X11 (Latin-1/keysymdef)
// keysym. Only covers what CODE_TO_VK actually sends — modifier keys
// (Ctrl/Alt/Shift/Meta) aren't in that table on the browser side either,
// so they're intentionally absent here too; this matches the existing
// Windows behavior (internal/input/inject_windows.go) rather than being
// a new gap.
var vkToKeysym = map[uint16]xproto.Keysym{
	0x30: 0x0030, 0x31: 0x0031, 0x32: 0x0032, 0x33: 0x0033, 0x34: 0x0034,
	0x35: 0x0035, 0x36: 0x0036, 0x37: 0x0037, 0x38: 0x0038, 0x39: 0x0039,

	0x41: 0x0061, 0x42: 0x0062, 0x43: 0x0063, 0x44: 0x0064, 0x45: 0x0065,
	0x46: 0x0066, 0x47: 0x0067, 0x48: 0x0068, 0x49: 0x0069, 0x4A: 0x006a,
	0x4B: 0x006b, 0x4C: 0x006c, 0x4D: 0x006d, 0x4E: 0x006e, 0x4F: 0x006f,
	0x50: 0x0070, 0x51: 0x0071, 0x52: 0x0072, 0x53: 0x0073, 0x54: 0x0074,
	0x55: 0x0075, 0x56: 0x0076, 0x57: 0x0077, 0x58: 0x0078, 0x59: 0x0079,
	0x5A: 0x007a,

	0x08: 0xFF08, // Backspace  (XK_BackSpace)
	0x09: 0xFF09, // Tab        (XK_Tab)
	0x0D: 0xFF0D, // Enter      (XK_Return)
	0x1B: 0xFF1B, // Escape     (XK_Escape)
	0x20: 0x0020, // Space
	0x21: 0xFF55, // PageUp     (XK_Prior)
	0x22: 0xFF56, // PageDown   (XK_Next)
	0x23: 0xFF57, // End        (XK_End)
	0x24: 0xFF50, // Home       (XK_Home)
	0x25: 0xFF51, // ArrowLeft  (XK_Left)
	0x26: 0xFF52, // ArrowUp    (XK_Up)
	0x27: 0xFF53, // ArrowRight (XK_Right)
	0x28: 0xFF54, // ArrowDown  (XK_Down)
	0x2E: 0xFFFF, // Delete     (XK_Delete)

	0x70: 0xFFBE, 0x71: 0xFFBF, 0x72: 0xFFC0, 0x73: 0xFFC1, // F1-F4
	0x74: 0xFFC2, 0x75: 0xFFC3, 0x76: 0xFFC4, 0x77: 0xFFC5, // F5-F8
	0x78: 0xFFC6, 0x79: 0xFFC7, 0x7A: 0xFFC8, 0x7B: 0xFFC9, // F9-F12

	0xBA: 0x003B, // Semicolon    ';' (XK_semicolon)
	0xBB: 0x003D, // Equal        '=' (XK_equal)
	0xBC: 0x002C, // Comma        ',' (XK_comma)
	0xBD: 0x002D, // Minus        '-' (XK_minus)
	0xBE: 0x002E, // Period       '.' (XK_period)
	0xBF: 0x002F, // Slash        '/' (XK_slash)
	0xC0: 0x0060, // Backquote    '`' (XK_grave)
	0xDB: 0x005B, // BracketLeft  '[' (XK_bracketleft)
	0xDC: 0x005C, // Backslash   '\\' (XK_backslash)
	0xDD: 0x005D, // BracketRight ']' (XK_bracketright)
	0xDE: 0x0027, // Quote        '\'' (XK_apostrophe)
}

func connect() {
	c, err := xgb.NewConn()
	if err != nil {
		connErr = fmt.Errorf("input: connect to X server: %w", err)
		return
	}
	if err := xtest.Init(c); err != nil {
		c.Close()
		connErr = fmt.Errorf("input: XTEST extension unavailable: %w", err)
		return
	}
	setup := xproto.Setup(c)
	if setup == nil || len(setup.Roots) == 0 {
		c.Close()
		connErr = errors.New("input: X server reported no screens")
		return
	}
	root = setup.DefaultScreen(c).Root

	count := byte(setup.MaxKeycode-setup.MinKeycode) + 1
	reply, err := xproto.GetKeyboardMapping(c, setup.MinKeycode, count).Reply()
	if err != nil {
		c.Close()
		connErr = fmt.Errorf("input: GetKeyboardMapping: %w", err)
		return
	}
	perKeycode := int(reply.KeysymsPerKeycode)
	keysymToKeycode = make(map[xproto.Keysym]xproto.Keycode, len(reply.Keysyms))
	if perKeycode > 0 {
		for i := 0; (i+1)*perKeycode <= len(reply.Keysyms); i++ {
			kc := xproto.Keycode(byte(setup.MinKeycode) + byte(i))
			for level := 0; level < perKeycode; level++ {
				ks := reply.Keysyms[i*perKeycode+level]
				if ks == 0 {
					continue
				}
				if _, exists := keysymToKeycode[ks]; !exists {
					keysymToKeycode[ks] = kc
				}
			}
		}
	}

	conn = c
}

func fakeInput(typ, detail byte, rootX, rootY int16) error {
	connOnce.Do(connect)
	if connErr != nil {
		return connErr
	}
	return xtest.FakeInputChecked(conn, typ, detail, xproto.TimeCurrentTime, root, rootX, rootY, 0).Check()
}

// MoveMouse moves the cursor to absolute screen pixel coordinates (x,
// y). screenW/screenH exist only for signature parity with the Windows
// implementation, which needs them to normalize into Win32's 0..65535
// MOUSEEVENTF_ABSOLUTE range — XTEST's FakeInput takes raw root-window
// pixel coordinates directly for an absolute MotionNotify (detail=0), no
// normalization needed.
func MoveMouse(x, y, screenW, screenH int32) error {
	const motionAbsolute = 0
	return fakeInput(xproto.MotionNotify, motionAbsolute, int16(x), int16(y))
}

// MouseButton presses or releases the left mouse button (button 1).
// Matches internal/input/inject_windows.go, which only wires up the
// left button too.
func MouseButton(down bool) error {
	const buttonLeft = 1
	typ := byte(xproto.ButtonPress)
	if !down {
		typ = xproto.ButtonRelease
	}
	return fakeInput(typ, buttonLeft, 0, 0)
}

func KeyPress(vk uint16, down bool) error {
	connOnce.Do(connect)
	if connErr != nil {
		return connErr
	}
	keysym, ok := vkToKeysym[vk]
	if !ok {
		return fmt.Errorf("input: no X11 keysym mapping for VK 0x%02X", vk)
	}
	keycode, ok := keysymToKeycode[keysym]
	if !ok {
		return fmt.Errorf("input: keysym 0x%04X has no keycode in the current X11 keyboard mapping", keysym)
	}
	typ := byte(xproto.KeyPress)
	if !down {
		typ = xproto.KeyRelease
	}
	return fakeInput(typ, byte(keycode), 0, 0)
}
