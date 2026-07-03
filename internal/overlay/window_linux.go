//go:build linux

// Overlay message window on X11, built on the same pure-Go X11 protocol
// library (jezek/xgb) already used by internal/capture and
// internal/input — no cgo.
//
// UNTESTED ON REAL HARDWARE, and with one known, deliberate difference
// from the Windows implementation (window_windows.go) rather than
// attempted exact parity: text is drawn with a core X11 bitmap font
// (ISO8859-1 / Latin-1 only) via the classic ImageText8 request, not a
// Unicode-capable renderer — Cyrillic or other non-Latin-1 text will not
// render correctly. Proper Unicode support would mean rasterizing text
// ourselves (e.g. golang.org/x/image/font + a bundled TTF) and blitting
// the result via PutImage instead of relying on X core fonts at all — a
// meaningfully bigger addition, so left as a follow-up rather than
// guessed at here.
//
// The background IS truly transparent when a compositing manager is
// running (Mutter, KWin, Picom, ...): the window uses a 32-bit ARGB
// visual with the background pixel's alpha byte set to 0, so a
// compositor skips blending it entirely and only the (fully-opaque)
// text pixels show — the X11 equivalent of the Windows version's
// LWA_COLORKEY trick. If the X server has no 32-bit visual available at
// all (rare — needs at least the Composite extension advertised, true
// on effectively every modern desktop), or no compositor is actually
// running to interpret that alpha, it falls back to a small solid dark
// box instead of failing outright — see findARGBVisual and the
// gcAlpha/bgAlpha handling below.
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
	overlayBgRGB = 0x1e1e1e // dark box background, matches the admin UI's own theme
	paddingX     = 10
	paddingY     = 6
)

// argbVisual describes a 32-bit-depth TrueColor visual suitable for a
// window with real per-pixel alpha.
type argbVisual struct {
	id    xproto.Visualid
	depth byte
}

// findARGBVisual scans the screen's advertised depths for one with 32
// bits (8 bits each of alpha/red/green/blue) and returns its first
// visual. Not every X server has one — it typically requires the
// Composite extension to be present, which is standard on modern
// desktops but not guaranteed — so callers must handle ok=false by
// falling back to an ordinary opaque window.
func findARGBVisual(screen *xproto.ScreenInfo) (v argbVisual, ok bool) {
	for _, d := range screen.AllowedDepths {
		if d.Depth == 32 && len(d.Visuals) > 0 {
			return argbVisual{id: d.Visuals[0].VisualId, depth: 32}, true
		}
	}
	return argbVisual{}, false
}

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

	// With a real 32-bit ARGB visual, the background pixel's alpha byte
	// is 0 (fully transparent to a compositor) while text/shadow are
	// drawn with alpha 0xFF (fully opaque) — so only the glyphs show.
	// Without one, everything falls back to the same opaque dark-box
	// look as before, on the screen's normal (depth-24) visual.
	visual, argbOK := findARGBVisual(screen)
	depth := screen.RootDepth
	visualID := screen.RootVisual
	var colormap xproto.Colormap
	valueMask := uint32(xproto.CwBackPixel | xproto.CwOverrideRedirect | xproto.CwEventMask)
	bgPixel := uint32(overlayBgRGB)
	alphaShift := uint32(0) // OR'd into text/shadow/background pixels; 0 on the non-ARGB path since there's no alpha byte to set

	if argbOK {
		cm, err := xproto.NewColormapId(conn)
		if err == nil {
			if err := xproto.CreateColormapChecked(conn, xproto.ColormapAllocNone, cm, root, visual.id).Check(); err == nil {
				colormap = cm
				depth = visual.depth
				visualID = visual.id
				valueMask |= xproto.CwBorderPixel | xproto.CwColormap
				bgPixel = 0 // alpha=0, rgb=0: fully transparent to a compositor
				alphaShift = 0xFF000000
			}
		}
	}
	if colormap != 0 {
		defer xproto.FreeColormap(conn, colormap)
	}

	wid, err := xproto.NewWindowId(conn)
	if err != nil {
		return err
	}
	valueList := []uint32{bgPixel}
	if valueMask&xproto.CwBorderPixel != 0 {
		valueList = append(valueList, 0) // border pixel required when depth differs from the parent (root)'s
	}
	valueList = append(valueList, 1 /* override-redirect */, xproto.EventMaskExposure)
	if valueMask&xproto.CwColormap != 0 {
		valueList = append(valueList, uint32(colormap))
	}
	err = xproto.CreateWindowChecked(conn, depth, wid, root,
		int16(ptr.RootX)+16, int16(ptr.RootY)+16, width, height, 0,
		xproto.WindowClassInputOutput, visualID,
		valueMask, valueList,
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
	textPixel := alphaShift | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
	// ImageText8 below fills each character cell's background with the
	// GC's background pixel, separately from the window's own CwBackPixel
	// clear — both must carry the same (transparent, on the ARGB path)
	// alpha, or the per-glyph-cell fill would paint an opaque box behind
	// the text and defeat the whole point of the ARGB visual above.
	bgGCPixel := bgPixel

	gc, err := xproto.NewGcontextId(conn)
	if err != nil {
		return err
	}
	err = xproto.CreateGCChecked(conn, gc, xproto.Drawable(wid),
		xproto.GcForeground|xproto.GcBackground|xproto.GcFont,
		[]uint32{textPixel, bgGCPixel, uint32(fid)},
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
