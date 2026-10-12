package desktop

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procEnumWindows     = user32.NewProc("EnumWindows")
	procGetWindowThread = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procFlashWindowEx   = user32.NewProc("FlashWindowEx")
	procGetForeground   = user32.NewProc("GetForegroundWindow")
)

type flashWInfo struct {
	cbSize    uint32
	hwnd      uintptr
	dwFlags   uint32
	uCount    uint32
	dwTimeout uint32
}

const (
	flashwAll       = 0x3 // caption and taskbar button
	flashwTimerNoFG = 0xC // until the window comes to the foreground
)

// FlashWindow flashes the app's taskbar button so an incoming offer gets
// noticed without the window jumping in front of whatever the user is doing.
func FlashWindow() {
	hwnd := mainWindow()
	if hwnd == 0 {
		return
	}
	if fg, _, _ := procGetForeground.Call(); fg == hwnd {
		return // already in front
	}
	info := flashWInfo{hwnd: hwnd, dwFlags: flashwAll | flashwTimerNoFG}
	info.cbSize = uint32(unsafe.Sizeof(info))
	procFlashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
}

// mainWindow finds this process's visible top-level window.
func mainWindow() uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumFound = 0
	procEnumWindows.Call(enumCallback, 0)
	return enumFound
}

// Windows allows only a limited number of callbacks per process, so the
// enumeration callback is created once and reports through these globals.
var (
	enumMu       sync.Mutex
	enumFound    uintptr
	enumCallback = syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var wpid uint32
		procGetWindowThread.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		if wpid == uint32(os.Getpid()) {
			if v, _, _ := procIsWindowVisible.Call(hwnd); v != 0 {
				enumFound = hwnd
				return 0 // stop
			}
		}
		return 1
	})
)

// WindowHidden reports whether the app's window is closed to the tray.
func WindowHidden() bool { return mainWindow() == 0 }

// AppInFront reports whether the app's window is the one in use.
func AppInFront() bool {
	hwnd := mainWindow()
	if hwnd == 0 {
		return false
	}
	fg, _, _ := procGetForeground.Call()
	return fg == hwnd
}
