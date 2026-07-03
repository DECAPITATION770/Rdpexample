//go:build windows

package input

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Mirrors the Win32 MOUSEINPUT/KEYBDINPUT/INPUT structs from winuser.h.
// Field order and sizes must match exactly on amd64: mouseInput is 32
// bytes (the union's determining member, since it's larger than
// keybdInput's 24), so `input` below — 4-byte type + 4-byte alignment
// padding + 32-byte union — comes out to 40 bytes, matching sizeof(INPUT)
// on Windows amd64. Do not add extra padding fields; SendInput validates
// cbSize against the real Win32 struct size and silently fails (returns
// 0) if it doesn't match exactly.
const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseEventMove     = 0x0001
	mouseEventLeftDown = 0x0002
	mouseEventLeftUp   = 0x0004
	mouseEventAbsolute = 0x8000

	keyEventKeyUp = 0x0002
)

type mouseInput struct {
	dx, dy      int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type keybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type input struct {
	inputType uint32
	mi        mouseInput // union slot; KeyPress reinterprets this memory as keybdInput
}

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

func sendRawInput(in input) error {
	size := unsafe.Sizeof(in)
	ret, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), size)
	if ret == 0 {
		return err
	}
	return nil
}

// MoveMouse moves the cursor to absolute screen pixel coordinates (x, y),
// given the width/height of the screen those coordinates were computed
// against. Callers MUST pass the exact dimensions internal/capture
// actually captured (not a separate GetSystemMetrics query) — on a
// display with Windows DPI scaling active, GetSystemMetrics can report a
// different (logical) resolution than what GDI captures (physical
// pixels), which throws off absolute positioning in a way that looks
// like the cursor just doesn't land where you clicked. Win32's
// MOUSEEVENTF_ABSOLUTE expects coordinates normalized to a 0..65535
// range, not raw pixels, so this normalizes using screenW/screenH before
// calling SendInput.
func MoveMouse(x, y, screenW, screenH int32) error {
	if screenW <= 1 || screenH <= 1 {
		return errors.New("input: invalid screen dimensions")
	}

	normX := int32((int64(x) * 65535) / int64(screenW-1))
	normY := int32((int64(y) * 65535) / int64(screenH-1))

	in := input{inputType: inputMouse, mi: mouseInput{
		dx: normX, dy: normY,
		dwFlags: mouseEventMove | mouseEventAbsolute,
	}}
	return sendRawInput(in)
}

func MouseButton(down bool) error {
	flag := uint32(mouseEventLeftDown)
	if !down {
		flag = mouseEventLeftUp
	}
	in := input{inputType: inputMouse, mi: mouseInput{dwFlags: flag}}
	return sendRawInput(in)
}

func KeyPress(vk uint16, down bool) error {
	var flags uint32
	if !down {
		flags = keyEventKeyUp
	}
	ki := keybdInput{wVk: vk, dwFlags: flags}
	in := input{inputType: inputKeyboard}
	// keybdInput (24 bytes) fits within mi's 32-byte union slot; this
	// reinterprets that memory the same way Windows' own C union would.
	*(*keybdInput)(unsafe.Pointer(&in.mi)) = ki
	return sendRawInput(in)
}
