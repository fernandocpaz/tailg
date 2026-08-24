//go:build windows

package platform

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procGetWindowText       = user32.NewProc("GetWindowTextW")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procMonitorFromWindow   = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo      = user32.NewProc("GetMonitorInfoW")
)

type winRect struct{ Left, Top, Right, Bottom int32 }
type monitorInfo struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
}

func visibleWindows() map[uintptr]string {
	result := map[uintptr]string{}
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		length, _, _ := procGetWindowTextLength.Call(hwnd)
		if length == 0 {
			return 1
		}
		buffer := make([]uint16, length+1)
		procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), length+1)
		result[hwnd] = syscall.UTF16ToString(buffer)
		return 1
	})
	procEnumWindows.Call(callback, 0)
	return result
}

func waitForWindows(before map[uintptr]string, titles []string, timeout time.Duration) []uintptr {
	deadline := time.Now().Add(timeout)
	found := map[uintptr]uintptr{}
	for time.Now().Before(deadline) {
		for handle, title := range visibleWindows() {
			if _, existed := before[handle]; existed {
				continue
			}
			for index, expected := range titles {
				if strings.Contains(strings.ToLower(title), strings.ToLower(expected)) {
					found[uintptr(index)] = handle
				}
			}
		}
		if len(found) == len(titles) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	result := make([]uintptr, 0, len(titles))
	for index := range titles {
		if handle := found[uintptr(index)]; handle != 0 {
			result = append(result, handle)
		}
	}
	return result
}

func activeWorkArea() Rect {
	foreground, _, _ := procGetForegroundWindow.Call()
	monitor, _, _ := procMonitorFromWindow.Call(foreground, 2)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	ok, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return Rect{0, 0, 1920, 1080}
	}
	return Rect{X: int(info.Work.Left), Y: int(info.Work.Top), Width: int(info.Work.Right - info.Work.Left), Height: int(info.Work.Bottom - info.Work.Top)}
}
func tileHandles(handles []uintptr) int {
	rectangles := TiledRectangles(len(handles), activeWorkArea())
	count := 0
	for index, handle := range handles {
		rect := rectangles[index]
		ok, _, _ := procSetWindowPos.Call(handle, 0, uintptr(rect.X), uintptr(rect.Y), uintptr(rect.Width), uintptr(rect.Height), 0x0040)
		if ok != 0 {
			count++
		}
	}
	return count
}
