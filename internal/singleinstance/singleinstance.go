package singleinstance

import (
	"errors"
	"strings"
	"time"

	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
)

const (
	mutexName          = `Local\ClipTranslator.SingleInstance`
	mainWindowClass    = `\o/ Walk_MainWindow_Class \o/`
	windowTitlePrefix  = "Clip Translator"
	activationAttempts = 20
	activationInterval = 150 * time.Millisecond
)

type Guard struct {
	handle windows.Handle
}

func Acquire() (*Guard, bool) {
	return acquire(mutexName)
}

func acquire(name string) (*Guard, bool) {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return &Guard{}, true
	}

	handle, err := windows.CreateMutex(nil, false, ptr)
	if handle == 0 {
		return &Guard{}, true
	}

	guard := &Guard{handle: handle}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return guard, false
	}
	return guard, true
}

func (g *Guard) Release() {
	if g == nil || g.handle == 0 {
		return
	}
	_ = windows.CloseHandle(g.handle)
	g.handle = 0
}

func ActivateExisting() bool {
	for i := 0; i < activationAttempts; i++ {
		if hwnd := findMainWindow(); hwnd != 0 {
			win.ShowWindow(hwnd, win.SW_RESTORE)
			win.ShowWindow(hwnd, win.SW_SHOW)
			win.SetForegroundWindow(hwnd)
			return true
		}
		time.Sleep(activationInterval)
	}
	return false
}

func ShowAlreadyRunningMessage() {
	text, err := windows.UTF16PtrFromString("Clip Translator가 이미 실행 중입니다.\n트레이 아이콘을 확인하세요.")
	if err != nil {
		return
	}
	caption, err := windows.UTF16PtrFromString("Clip Translator")
	if err != nil {
		return
	}
	win.MessageBox(0, text, caption, win.MB_OK|win.MB_ICONINFORMATION)
}

func findMainWindow() win.HWND {
	var found win.HWND
	callback := windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		current := win.HWND(hwnd)

		class := make([]uint16, 256)
		classLen, err := win.GetClassName(current, &class[0], len(class))
		if err != nil || !strings.EqualFold(windows.UTF16ToString(class[:classLen]), mainWindowClass) {
			return 1
		}

		title := make([]uint16, 512)
		titleLen := win.GetWindowText(current, &title[0], int32(len(title)))
		if titleLen <= 0 || !strings.HasPrefix(windows.UTF16ToString(title[:int(titleLen)]), windowTitlePrefix) {
			return 1
		}

		found = current
		return 0
	})
	_ = windows.EnumWindows(callback, nil)
	return found
}
