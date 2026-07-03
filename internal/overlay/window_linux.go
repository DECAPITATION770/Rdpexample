//go:build linux

// Overlay message window on X11, built on the same pure-Go X11 protocol
// library (jezek/xgb) already used by internal/capture and
// internal/input — no cgo.
//
// UNTESTED ON REAL HARDWARE, and with two known, deliberate differences
// from the Windows implementation (window_windows.go) rather than
// attempted exact parity:
//
//  1. No transparent/color-keyed background: getting a truly
//     transparent, borderless "just floating text" look on X11 needs a
//     32-bit ARGB visual plus the RENDER extension (or a compositing
//     manager cooperating via one), which is a lot of additional
//     unverifiable protocol code to take on blind. This instead draws a
//     small solid dark box with the colored text inside, and best-effort
//     fades the whole box out via the _NET_WM_WINDOW_OPACITY property —
//     an EWMH convention most compositing window managers (Mutter, KWin,
//     Picom, ...) honor, but which silently does nothing if no
//     compositor is running (the box just stays opaque until destroyed
//     instead of fading — a graceful degradation, not a crash).
//  2. Text is drawn with a core X11 bitmap font (ISO8859-1 / Latin-1
//     only) via the classic ImageText8 request, not a Unicode-capable
//     renderer — Cyrillic or other non-Latin-1 text will not render
//     correctly. Proper Unicode support would mean rasterizing text
//     ourselves (e.g. golang.org/x/image/font + a bundled TTF) and
//     blitting the result via PutImage instead of relying on X core
//     fonts at all — a meaningfully bigger addition, so left as a
//     follow-up rather than guessed at here.
package overlay

import (
	"fmt"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
)

// fontName is "fixed" — the one core font virtually guaranteed to exist
// on any X server, since X itself falls back to it as the default. Using
// anything fancier risks OpenFont failing on a minimal install with no
// way to verify here.
const fontName = "fixed"

const (
	overlayBgPixel = 0x1e1e1e // dark box background, matches the admin UI's own theme
	paddingX       = 10
	paddingY       = 6
)

func stringToChar2b(s string) []xproto.Char2b {
	out := make([]xproto.Char2b, len(s))
	for i := 0; i < len(s); i++ {
		out[i] = xproto.Char2b{Byte1: 0, Byte2: s[i]}
	}
	return out
}

// ShowMessage creates a small, click-through, always-on-top box near the
// current cursor position showing text in hexColor ("#RRGGBB"; empty or
// malformed falls back to white via ParseHexColor), fades it out over
// fadeDuration (best effort — see the package doc comment above), and
// destroys it when done. Blocks the calling goroutine for the duration
// of the fade — callers should run this in its own goroutine per
// incoming overlay message, same as the Windows implementation.
func ShowMessage(text string, fadeDuration time.Duration, hexColor string) error {
	conn, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("overlay: connect to X server: %w", err)
	}
	defer conn.Close()

	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 {
		return fmt.Errorf("overlay: X server reported no screens")
	}
	screen := setup.DefaultScreen(conn)
	root := screen.Root

	shapeOK := shape.Init(conn) == nil // click-through is best-effort; missing SHAPE extension isn't fatal

	ptr, err := xproto.QueryPointer(conn, root).Reply()
	if err != nil {
		return fmt.Errorf("overlay: QueryPointer: %w", err)
	}

	fid, err := xproto.NewFontId(conn)
	if err != nil {
		return err
	}
	if err := xproto.OpenFontChecked(conn, fid, uint16(len(fontName)), fontName).Check(); err != nil {
		return fmt.Errorf("overlay: OpenFont %q: %w", fontName, err)
	}
	defer xproto.CloseFont(conn, fid)

	extents, err := xproto.QueryTextExtents(conn, xproto.Fontable(fid), stringToChar2b(text), uint16(len(text))).Reply()
	if err != nil {
		return fmt.Errorf("overlay: QueryTextExtents: %w", err)
	}
	lineHeight := int32(extents.FontAscent) + int32(extents.FontDescent)

	width := uint16(extents.OverallWidth) + paddingX*2
	height := uint16(lineHeight) + paddingY*2

	wid, err := xproto.NewWindowId(conn)
	if err != nil {
		return err
	}
	err = xproto.CreateWindowChecked(conn, screen.RootDepth, wid, root,
		int16(ptr.RootX)+16, int16(ptr.RootY)+16, width, height, 0,
		xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwBackPixel|xproto.CwOverrideRedirect|xproto.CwEventMask,
		[]uint32{overlayBgPixel, 1, xproto.EventMaskExposure},
	).Check()
	if err != nil {
		return fmt.Errorf("overlay: CreateWindow: %w", err)
	}
	defer xproto.DestroyWindow(conn, wid)

	if shapeOK {
		// Empty input-shape region: the window still displays, but no
		// pointer event ever targets it, so it never blocks a click
		// meant for whatever is underneath — the X11 equivalent of
		// Windows' WS_EX_TRANSPARENT on the overlay window.
		_ = shape.RectanglesChecked(conn, shape.SoSet, shape.SkInput, xproto.ClipOrderingUnsorted, wid, 0, 0, nil).Check()
	}

	r, g, b, _ := ParseHexColor(hexColor) // falls back to white on empty/malformed
	textPixel := uint32(r)<<16 | uint32(g)<<8 | uint32(b)

	gc, err := xproto.NewGcontextId(conn)
	if err != nil {
		return err
	}
	err = xproto.CreateGCChecked(conn, gc, xproto.Drawable(wid),
		xproto.GcForeground|xproto.GcBackground|xproto.GcFont,
		[]uint32{textPixel, overlayBgPixel, uint32(fid)},
	).Check()
	if err != nil {
		return fmt.Errorf("overlay: CreateGC: %w", err)
	}
	defer xproto.FreeGC(conn, gc)

	if err := xproto.MapWindowChecked(conn, wid).Check(); err != nil {
		return fmt.Errorf("overlay: MapWindow: %w", err)
	}
	baseline := int16(paddingY) + int16(extents.FontAscent)
	if err := xproto.ImageText8Checked(conn, byte(len(text)), xproto.Drawable(wid), gc, paddingX, baseline, text).Check(); err != nil {
		return fmt.Errorf("overlay: ImageText8: %w", err)
	}

	opacityReply, atomErr := xproto.InternAtom(conn, false, uint16(len("_NET_WM_WINDOW_OPACITY")), "_NET_WM_WINDOW_OPACITY").Reply()
	fadeSupported := atomErr == nil && opacityReply != nil

	timer := NewFadeTimer(fadeDuration)
	start := time.Now()
	ticker := time.NewTicker(33 * time.Millisecond) // ~30fps fade, matching the Windows implementation
	defer ticker.Stop()

	for range ticker.C {
		if fadeSupported {
			elapsed := time.Since(start)
			opacity := timer.Opacity(elapsed)
			val := uint32(opacity * 0xFFFFFFFF)
			buf := make([]byte, 4)
			xgb.Put32(buf, val)
			_ = xproto.ChangePropertyChecked(conn, xproto.PropModeReplace, wid, opacityReply.Atom, xproto.AtomCardinal, 32, 1, buf).Check()
		}
		if timer.IsExpired(time.Since(start)) {
			return nil
		}
	}
	return nil
}
