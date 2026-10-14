//go:build windows

package theme

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

type coord struct{ X, Y int16 }
type smallRect struct{ Left, Top, Right, Bottom int16 }
type screenBufferInfoEx struct {
	Size                       uint32
	BufferSize, CursorPosition coord
	Attributes                 uint16
	Window                     smallRect
	MaximumWindowSize          coord
	PopupAttributes            uint16
	FullscreenSupported        int32
	ColorTable                 [16]uint32
}

func terminalColors(out *os.File) (string, string, bool) {
	// termenv v0.16 hardcodes black/gray on Windows. Query the native console
	// attributes and the current color table instead, including custom schemes.
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfoEx")
	if proc.Find() != nil {
		return "", "", false
	}
	info := screenBufferInfoEx{}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, _ := proc.Call(out.Fd(), uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return "", "", false
	}
	hex := func(color uint32) string {
		return fmt.Sprintf("#%02X%02X%02X", color&255, (color>>8)&255, (color>>16)&255)
	}
	return hex(info.ColorTable[info.Attributes&15]), hex(info.ColorTable[(info.Attributes>>4)&15]), true
}
