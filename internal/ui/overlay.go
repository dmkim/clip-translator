package ui

import (
	"errors"
	"syscall"
	"unsafe"

	"github.com/tailscale/win"
)

const (
	toastWidth       = 720
	toastHeight      = 64
	toastTop         = 40
	toastAlpha       = 220
	lwaAlpha         = 0x00000002
	toastWindowClass = "ClipTranslatorToast"
)

var (
	user32Lib                  = syscall.NewLazyDLL("user32.dll")
	setLayeredWindowAttributes = user32Lib.NewProc("SetLayeredWindowAttributes")
	toastWndProcPtr            = syscall.NewCallback(toastWndProc)
	toastClassRegistered       bool
	toastRegistry              = map[win.HWND]*toast{}
)

type toast struct {
	hwnd   win.HWND
	font   win.HFONT
	brush  win.HBRUSH
	text   string
	width  int32
	height int32
}

func newToast() (*toast, error) {
	if err := registerToastClass(); err != nil {
		return nil, err
	}

	className, err := syscall.UTF16PtrFromString(toastWindowClass)
	if err != nil {
		return nil, err
	}

	x, y := toastPosition(toastWidth, toastHeight)
	hwnd := win.CreateWindowEx(
		win.WS_EX_LAYERED|win.WS_EX_TOOLWINDOW|win.WS_EX_NOACTIVATE|win.WS_EX_TRANSPARENT|win.WS_EX_TOPMOST,
		className,
		nil,
		win.WS_POPUP,
		x, y, toastWidth, toastHeight,
		0, 0,
		win.GetModuleHandle(nil),
		nil,
	)
	if hwnd == 0 {
		return nil, errors.New("토스트 창 생성 실패")
	}

	t := &toast{hwnd: hwnd, width: toastWidth, height: toastHeight}

	var lf win.LOGFONT
	lf.LfHeight = -24
	lf.LfWeight = win.FW_BOLD
	lf.LfCharSet = win.DEFAULT_CHARSET
	lf.LfQuality = win.CLEARTYPE_QUALITY
	copy(lf.LfFaceName[:], syscall.StringToUTF16("Segoe UI"))
	t.font = win.CreateFontIndirect(&lf)

	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(24, 24, 24)}
	t.brush = win.CreateBrushIndirect(&lb)

	toastRegistry[hwnd] = t
	setLayeredWindowAttributes.Call(uintptr(hwnd), 0, toastAlpha, lwaAlpha)
	win.ShowWindow(hwnd, win.SW_HIDE)

	return t, nil
}

func registerToastClass() error {
	if toastClassRegistered {
		return nil
	}

	className, err := syscall.UTF16PtrFromString(toastWindowClass)
	if err != nil {
		return err
	}

	wc := win.WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
		LpfnWndProc:   toastWndProcPtr,
		HInstance:     win.GetModuleHandle(nil),
		LpszClassName: className,
	}
	// RegisterClassEx returns 0 when the class already exists; CreateWindowEx
	// can still use it in that case.
	win.RegisterClassEx(&wc)
	toastClassRegistered = true
	return nil
}

func toastWndProc(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case win.WM_NCHITTEST:
		return ^uintptr(0)
	case win.WM_ERASEBKGND:
		return 1
	case win.WM_PAINT:
		if t := toastFromHandle(hwnd); t != nil {
			t.paint()
			return 0
		}
	}

	return win.DefWindowProc(hwnd, msg, wParam, lParam)
}

func toastFromHandle(hwnd win.HWND) *toast {
	return toastRegistry[hwnd]
}

func (t *toast) paint() {
	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(t.hwnd, &ps)
	if hdc == 0 {
		return
	}
	defer win.EndPaint(t.hwnd, &ps)

	var rect win.RECT
	if !win.GetClientRect(t.hwnd, &rect) {
		return
	}

	if t.brush != 0 {
		win.FillRect(hdc, &rect, t.brush)
	}
	if t.font == 0 || t.text == "" {
		return
	}

	old := win.SelectObject(hdc, win.HGDIOBJ(t.font))
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.RGB(255, 255, 255))
	text, _ := syscall.UTF16PtrFromString(t.text)
	win.DrawTextEx(hdc, text, -1, &rect,
		win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE|win.DT_END_ELLIPSIS, nil)
	win.SelectObject(hdc, old)
}

func (t *toast) show(text string) {
	if t == nil || t.hwnd == 0 {
		return
	}
	t.text = text
	x, y := toastPosition(t.width, t.height)
	win.SetWindowPos(t.hwnd, win.HWND_TOPMOST, x, y, t.width, t.height,
		win.SWP_NOACTIVATE|win.SWP_SHOWWINDOW)
	win.InvalidateRect(t.hwnd, nil, true)
}

func (t *toast) hide() {
	if t == nil || t.hwnd == 0 {
		return
	}
	win.ShowWindow(t.hwnd, win.SW_HIDE)
}

func (t *toast) dispose() {
	if t == nil || t.hwnd == 0 {
		return
	}
	win.ShowWindow(t.hwnd, win.SW_HIDE)
	delete(toastRegistry, t.hwnd)
	win.DestroyWindow(t.hwnd)
	t.hwnd = 0
	if t.font != 0 {
		win.DeleteObject(win.HGDIOBJ(t.font))
		t.font = 0
	}
	if t.brush != 0 {
		win.DeleteObject(win.HGDIOBJ(t.brush))
		t.brush = 0
	}
}

func toastPosition(width, height int32) (int32, int32) {
	hmon := win.MonitorFromWindow(win.GetForegroundWindow(), win.MONITOR_DEFAULTTONEAREST)
	var mi win.MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if hmon != 0 && win.GetMonitorInfo(hmon, &mi) {
		left := mi.RcMonitor.Left
		right := mi.RcMonitor.Right
		top := mi.RcMonitor.Top
		return left + ((right-left)-width)/2, top + toastTop
	}

	screenWidth := win.GetSystemMetrics(win.SM_CXSCREEN)
	return (screenWidth - width) / 2, toastTop
}
