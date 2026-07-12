//go:build windows

package capture

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procGetCursorPos = user32.NewProc("GetCursorPos")
)

type cursorPoint struct{ X, Y int32 }

// cursorScreenPos returns the current mouse position in the same Win32
// screen-coordinate space GDI's capture already uses. The actual capture
// and encode is shared across platforms in capture.go; only this
// per-OS cursor lookup differs. See GrabPrimaryFrame's doc comment there
// for why the capture's own bounds (not GetSystemMetrics) drive mouse
// coordinate normalization under DPI scaling.
func cursorScreenPos() (x, y int32, ok bool) {
	var p cursorPoint
	ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p.X, p.Y, ret != 0
}
