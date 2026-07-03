//go:build windows

package overlay

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wsExLayered     = 0x00080000
	wsExTransparent = 0x00000020
	wsExTopmost     = 0x00000008
	wsExToolWindow  = 0x00000080
	wsPopup         = 0x80000000

	lwaColorKey = 0x00000001
	lwaAlpha    = 0x00000002

	swShow = 5

	wmPaint     = 0x000F
	wmNCDestroy = 0x0082

	dtSingleLine = 0x00000020
	dtNoClip     = 0x00000100

	bkModeTransparent = 1

	pmRemove = 0x0001
)

func rgb(r, g, b byte) uint32 {
	return uint32(r) | uint32(g)<<8 | uint32(b)<<16
}

var (
	shadowColor     = rgb(0, 0, 0) // black, offset behind the text for legibility
	backgroundColor = rgb(0, 0, 0) // color-keyed fully transparent — no visible box
)

var (
	user32                    = windows.NewLazySystemDLL("user32.dll")
	gdi32                     = windows.NewLazySystemDLL("gdi32.dll")
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassEx       = user32.NewProc("RegisterClassExW")
	procCreateWindowEx        = user32.NewProc("CreateWindowExW")
	procDefWindowProc         = user32.NewProc("DefWindowProcW")
	procSetLayeredWindowAttrs = user32.NewProc("SetLayeredWindowAttributes")
	procShowWindow            = user32.NewProc("ShowWindow")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procBeginPaint            = user32.NewProc("BeginPaint")
	procEndPaint              = user32.NewProc("EndPaint")
	procGetDC                 = user32.NewProc("GetDC")
	procReleaseDC             = user32.NewProc("ReleaseDC")
	procDrawText              = user32.NewProc("DrawTextW")
	procPeekMessage           = user32.NewProc("PeekMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessage       = user32.NewProc("DispatchMessageW")
	procGetModuleHandle       = kernel32.NewProc("GetModuleHandleW")

	procSetBkMode            = gdi32.NewProc("SetBkMode")
	procSetTextColor         = gdi32.NewProc("SetTextColor")
	procGetTextExtentPoint32 = gdi32.NewProc("GetTextExtentPoint32W")
	procCreateSolidBrush     = gdi32.NewProc("CreateSolidBrush")
)

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type sizeXY struct{ CX, CY int32 }

// msgStruct mirrors Win32's MSG. Field order matches the C layout, and
// Go's natural alignment on amd64 inserts the same padding C would (4
// bytes after Message so WParam lands on an 8-byte boundary) — same
// approach as wndClassExW below.
type msgStruct struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type paintStruct struct {
	HDC         uintptr
	FErase      int32
	RCPaint     rect
	FRestore    int32
	FIncUpdate  int32
	RGBReserved [32]byte
}

// wndClassExW mirrors WNDCLASSEXW. Go's natural struct alignment on amd64
// matches the real C layout field-for-field here (all pointer-sized
// fields already fall on 8-byte boundaries), so this comes out to the
// same 80 bytes as sizeof(WNDCLASSEXW) with no manual padding needed.
type wndClassExW struct {
	CBSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CBClsExtra    int32
	CBWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HBrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

const className = "RDPToolOverlayWindow"

// overlayWindowState is the per-window data the shared window procedure
// needs to paint on WM_PAINT — text and its color, since each message
// can request its own.
type overlayWindowState struct {
	text  string
	color uint32 // Windows COLORREF (0x00BBGGRR), see rgb()
}

var (
	registerOnce sync.Once
	registerErr  error

	// windowStates holds the text/color for each live overlay hwnd,
	// looked up by the shared window procedure on WM_PAINT. A real Win32
	// union/GWLP_USERDATA slot would also work, but a map keyed by hwnd
	// is simpler to get right without more unsafe pointer juggling.
	windowStates sync.Map // uintptr(hwnd) -> overlayWindowState
)

func registerClass() error {
	registerOnce.Do(func() {
		hInstance, _, _ := procGetModuleHandle.Call(0)

		brush, _, _ := procCreateSolidBrush.Call(uintptr(backgroundColor))

		classNamePtr, err := windows.UTF16PtrFromString(className)
		if err != nil {
			registerErr = err
			return
		}

		wc := wndClassExW{
			CBSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			LpfnWndProc:   windowProcCallback,
			HInstance:     hInstance,
			HBrBackground: brush,
			LpszClassName: classNamePtr,
		}

		ret, _, callErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		if ret == 0 {
			registerErr = callErr
		}
	})
	return registerErr
}

var windowProcCallback = syscall.NewCallback(func(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmPaint:
		paintOverlay(hwnd)
		return 0
	case wmNCDestroy:
		windowStates.Delete(hwnd)
		return 0
	}
	ret, _, _ := procDefWindowProc.Call(hwnd, uintptr(msg), wparam, lparam)
	return ret
})

func paintOverlay(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	stateVal, _ := windowStates.Load(hwnd)
	state, _ := stateVal.(overlayWindowState)
	textPtr, textLen, err := utf16PtrAndLen(state.text)
	if err != nil {
		return
	}

	procSetBkMode.Call(hdc, bkModeTransparent)

	// A 1px drop shadow gives the floating text legibility over any
	// desktop background without drawing a visible box/frame.
	shadowRect := rect{Left: 1, Top: 1, Right: ps.RCPaint.Right, Bottom: ps.RCPaint.Bottom}
	procSetTextColor.Call(hdc, uintptr(shadowColor))
	procDrawText.Call(hdc, uintptr(unsafe.Pointer(textPtr)), uintptr(textLen), uintptr(unsafe.Pointer(&shadowRect)), dtSingleLine|dtNoClip)

	mainRect := rect{Left: 0, Top: 0, Right: ps.RCPaint.Right, Bottom: ps.RCPaint.Bottom}
	procSetTextColor.Call(hdc, uintptr(state.color))
	procDrawText.Call(hdc, uintptr(unsafe.Pointer(textPtr)), uintptr(textLen), uintptr(unsafe.Pointer(&mainRect)), dtSingleLine|dtNoClip)
}

// utf16PtrAndLen returns a null-terminated UTF-16 pointer suitable for
// Win32 *W calls, plus the code-unit count Win32 expects for nCount
// parameters (which is NOT the same as the Unicode rune count for any
// text containing surrogate-pair characters, e.g. most emoji).
func utf16PtrAndLen(s string) (*uint16, int, error) {
	units, err := windows.UTF16FromString(s)
	if err != nil {
		return nil, 0, err
	}
	return &units[0], len(units) - 1, nil // drop the null terminator from the count
}

func measureText(s string) (width, height int32, err error) {
	textPtr, textLen, err := utf16PtrAndLen(s)
	if err != nil {
		return 0, 0, err
	}
	hdc, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, hdc)

	var sz sizeXY
	procGetTextExtentPoint32.Call(hdc, uintptr(unsafe.Pointer(textPtr)), uintptr(textLen), uintptr(unsafe.Pointer(&sz)))
	return sz.CX, sz.CY, nil
}

// createOverlayWindow registers the shared window class on first use,
// measures the text to size the window, and creates a borderless,
// click-through, always-on-top popup window near (x, y) showing it in
// the given color.
func createOverlayWindow(text string, color uint32, x, y int32) (uintptr, error) {
	if err := registerClass(); err != nil {
		return 0, err
	}

	textW, textH, err := measureText(text)
	if err != nil {
		return 0, err
	}
	const padding = 8
	width := textW + padding*2
	height := textH + padding*2

	hInstance, _, _ := procGetModuleHandle.Call(0)
	classNamePtr, err := windows.UTF16PtrFromString(className)
	if err != nil {
		return 0, err
	}

	exStyle := uintptr(wsExLayered | wsExTransparent | wsExTopmost | wsExToolWindow)
	hwnd, _, callErr := procCreateWindowEx.Call(
		exStyle,
		uintptr(unsafe.Pointer(classNamePtr)),
		0, // no window title needed — WS_POPUP has no title bar
		uintptr(wsPopup),
		uintptr(x+16), uintptr(y+16),
		uintptr(width), uintptr(height),
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return 0, callErr
	}

	windowStates.Store(hwnd, overlayWindowState{text: text, color: color})
	return hwnd, nil
}

// ShowMessage creates a click-through layered window near the current
// cursor position showing text in hexColor ("#RRGGBB"; empty or
// malformed falls back to white via ParseHexColor), fades it out over
// fadeDuration using FadeTimer, and destroys it when done. Blocks the
// calling goroutine for the duration of the fade — callers should run
// this in its own goroutine per incoming overlay message.
func ShowMessage(text string, fadeDuration time.Duration, hexColor string) error {
	// A window's message queue is thread-affine in Win32: only the
	// thread that created the window can pump messages for it via
	// PeekMessage/DispatchMessage. Go goroutines aren't pinned to an OS
	// thread by default, so without LockOSThread the runtime could hop
	// this goroutine across threads between CreateWindowExW and the
	// message loop below — and without a message loop running on the
	// right thread at all, WM_PAINT never reaches paintOverlay: the
	// window just sits there showing whatever blank surface Windows
	// defaults to, never draws the text, and never processes WM_DESTROY
	// either. Locking for the whole call keeps window creation and the
	// message pump on the same OS thread throughout.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))

	r, g, b, _ := ParseHexColor(hexColor) // falls back to white on empty/malformed
	hwnd, err := createOverlayWindow(text, rgb(r, g, b), cursor.X, cursor.Y)
	if err != nil {
		return err
	}
	defer procDestroyWindow.Call(hwnd)

	// LWA_COLORKEY makes the black background fully transparent (so only
	// the text is visible, no box/frame); LWA_ALPHA is combined in the
	// same call so the visible text can still be faded via alpha — both
	// flags must be passed on every SetLayeredWindowAttributes call or
	// the color-key transparency is dropped.
	procSetLayeredWindowAttrs.Call(hwnd, uintptr(backgroundColor), 255, lwaColorKey|lwaAlpha)
	procShowWindow.Call(hwnd, swShow)

	timer := NewFadeTimer(fadeDuration)
	start := time.Now()
	ticker := time.NewTicker(33 * time.Millisecond) // ~30fps fade
	defer ticker.Stop()

	var msg msgStruct
	for range ticker.C {
		// Drain this thread's message queue so WM_PAINT (and everything
		// else, e.g. the eventual WM_NCDESTROY) actually gets dispatched
		// to windowProcCallback — see the LockOSThread comment above for
		// why this has to happen on this same thread.
		for {
			has, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
			if has == 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}

		elapsed := time.Since(start)
		opacity := timer.Opacity(elapsed)
		alpha := uintptr(opacity * 255)
		procSetLayeredWindowAttrs.Call(hwnd, uintptr(backgroundColor), alpha, lwaColorKey|lwaAlpha)
		if timer.IsExpired(elapsed) {
			return nil
		}
	}
	return nil
}
