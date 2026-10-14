//go:build windows

package main

import (
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Native window control (Win32): a frameless window with the application's own
// title bar, a polling based drag implementation for the HTML title bar, and
// minimize/maximize/close actions triggered from the UI.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procShowWindow        = user32.NewProc("ShowWindow")
	procIsZoomed          = user32.NewProc("IsZoomed")
	procPostMessageW      = user32.NewProc("PostMessageW")
	procSendMessageW      = user32.NewProc("SendMessageW")
	procGetCursorPos      = user32.NewProc("GetCursorPos")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procGetAsyncKeyState  = user32.NewProc("GetAsyncKeyState")
	procLoadImageW        = user32.NewProc("LoadImageW")
	procGetModuleHandleW  = kernel32.NewProc("GetModuleHandleW")
	procDwmSetAttribute   = dwmapi.NewProc("DwmSetWindowAttribute")
	procCallWindowProcW   = user32.NewProc("CallWindowProcW")
)

const (
	wsCaption     = 0x00C00000
	wsThickFrame  = 0x00040000
	wsMinimizeBox = 0x00020000
	wsMaximizeBox = 0x00010000
	wsSysMenu     = 0x00080000
	wsExAppWindow = 0x00040000

	wmNCCalcSize = 0x0083
	wmSize       = 0x0005

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	swMinimize = 6
	swMaximize = 3
	swRestore  = 9

	wmClose   = 0x0010
	wmSetIcon = 0x0080

	iconSmall = 0
	iconBig   = 1

	imageIcon       = 1
	lrDefaultSize   = 0x0040
	lrShared        = 0x8000
	vkLeftButton    = 0x01
	iconResourceID  = 1
	dragFrameMillis = 8
	dragMinMargin   = 90
	dragMaxOffsetY  = 30

	// Smallest window the layout still works in, used while resizing by hand.
	minWindowWidth  = 900
	minWindowHeight = 620
)

const (
	dwmwaWindowCornerPreference = 33
	dwmcpRound                  = 2
)

// GWLP_STYLE (-16), GWLP_EXSTYLE (-20) and GWLP_WNDPROC (-4) as unsigned values.
var (
	gwlStyle   = ^uintptr(15)
	gwlExStyle = ^uintptr(19)
	gwlWndProc = ^uintptr(3)
)

// wndProcCallback holds the subclassed window procedure; the callback must stay
// reachable for the lifetime of the window.
var wndProcCallback uintptr
var previousWndProc uintptr

type winPoint struct{ x, y int32 }

type winRect struct{ left, top, right, bottom int32 }

type windowController struct {
	hwnd     uintptr
	dragging atomic.Bool
	resizing atomic.Bool
}

func (w *windowController) set(hwnd uintptr) {
	w.hwnd = hwnd
}

// makeFrameless removes the classic Windows title bar, keeps the resize frame
// and the Windows 11 rounded corners, and makes the client area cover the whole
// window. Without that last step Windows keeps an invisible resize border
// around a frameless window, which shows up as a margin around the interface.
// Must run on the UI thread.
func makeFrameless(hwnd uintptr) {
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlStyle)
	newStyle := (style &^ uintptr(wsCaption)) |
		wsThickFrame | wsMinimizeBox | wsMaximizeBox | wsSysMenu
	procSetWindowLongPtrW.Call(hwnd, gwlStyle, newStyle)

	exStyle, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlExStyle)
	procSetWindowLongPtrW.Call(hwnd, gwlExStyle, exStyle|wsExAppWindow)

	subclassWindow(hwnd)

	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged)

	round := int32(dwmcpRound)
	procDwmSetAttribute.Call(
		hwnd,
		dwmwaWindowCornerPreference,
		uintptr(unsafe.Pointer(&round)),
		unsafe.Sizeof(round),
	)

	applyWindowIcon(hwnd)
}

// subclassWindow installs the window procedure that gives the client area the
// full window rectangle (WM_NCCALCSIZE). Everything else is forwarded.
func subclassWindow(hwnd uintptr) {
	if wndProcCallback == 0 {
		wndProcCallback = syscall.NewCallback(windowProc)
	}
	previous, _, _ := procSetWindowLongPtrW.Call(hwnd, gwlWndProc, wndProcCallback)
	previousWndProc = previous
}

func windowProc(hwnd, message, wparam, lparam uintptr) uintptr {
	switch message {
	case wmNCCalcSize:
		if wparam != 0 {
			// The whole window is client area: no invisible resize border, so
			// the web interface sits flush against the window edges. Resizing
			// is handled by the interface (see startResize).
			return 0
		}
	}
	result, _, _ := procCallWindowProcW.Call(previousWndProc, hwnd, message, wparam, lparam)
	return result
}

// applyWindowIcon sets the icon embedded in the executable (see
// icon_windows_amd64.syso) for the task bar and the Alt+Tab switcher.
func applyWindowIcon(hwnd uintptr) {
	instance, _, _ := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return
	}
	icon, _, _ := procLoadImageW.Call(
		instance,
		iconResourceID,
		imageIcon,
		0,
		0,
		lrDefaultSize|lrShared,
	)
	if icon == 0 {
		return
	}
	procSendMessageW.Call(hwnd, wmSetIcon, iconBig, icon)
	procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, icon)
}

func (w *windowController) handleAction(action, edge string) {
	switch action {
	case "minimize":
		if w.hwnd != 0 {
			procShowWindow.Call(w.hwnd, swMinimize)
		}
	case "maximize":
		if w.hwnd == 0 {
			return
		}
		if maximized, _, _ := procIsZoomed.Call(w.hwnd); maximized != 0 {
			procShowWindow.Call(w.hwnd, swRestore)
		} else {
			procShowWindow.Call(w.hwnd, swMaximize)
		}
	case "close":
		if w.hwnd != 0 {
			procPostMessageW.Call(w.hwnd, wmClose, 0, 0)
		}
	case "drag":
		w.startDrag()
	case "resize":
		// The interface reports which edge was grabbed; the window is resized
		// here, because the web view covers the whole window and Windows
		// therefore never sees the frame edges.
		w.startResize(edge)
	}
}

// startDrag moves the window with the mouse until the left button is released.
//
// The WebView2 child window owns the mouse, so the classic
// ReleaseCapture + WM_NCLBUTTONDOWN trick does not work here. Instead the
// cursor position is polled and the window is repositioned directly.
func (w *windowController) startDrag() {
	if w.hwnd == 0 || !w.dragging.CompareAndSwap(false, true) {
		return
	}
	hwnd := w.hwnd
	go func() {
		defer w.dragging.Store(false)
		runDrag(dragEnvironment{
			buttonDown: func() bool {
				pressed, _, _ := procGetAsyncKeyState.Call(vkLeftButton)
				return pressed&0x8000 != 0
			},
			cursor: cursorPosition,
			bounds: func() (winRect, bool) { return windowBounds(hwnd) },
			restore: func() bool {
				maximized, _, _ := procIsZoomed.Call(hwnd)
				if maximized == 0 {
					return false
				}
				procShowWindow.Call(hwnd, swRestore)
				return true
			},
			move: func(x, y int32) {
				procSetWindowPos.Call(
					hwnd,
					0,
					uintptr(x),
					uintptr(y),
					0,
					0,
					swpNoSize|swpNoZOrder|swpNoActivate,
				)
			},
			sleep: func(duration time.Duration) { time.Sleep(duration) },
		})
	}()
}

// dragEnvironment collects the platform calls used by the drag loop so the
// logic can be unit tested without a real mouse.
type dragEnvironment struct {
	buttonDown func() bool
	cursor     func() (winPoint, bool)
	bounds     func() (winRect, bool)
	restore    func() bool
	move       func(x, y int32)
	sleep      func(time.Duration)
}

// runDrag follows the cursor with the window until the button is released.
func runDrag(env dragEnvironment) {
	cursor, ok := env.cursor()
	if !ok {
		return
	}

	if env.restore() {
		// Give Windows a moment to finish the restore before measuring.
		env.sleep(120 * time.Millisecond)
	}

	rect, ok := env.bounds()
	if !ok {
		return
	}
	width := rect.right - rect.left
	offsetX := cursor.x - rect.left
	offsetY := cursor.y - rect.top

	// Keep the grab point inside the window, which matters after restoring a
	// maximized window.
	if width > 2*dragMinMargin {
		if offsetX < dragMinMargin {
			offsetX = dragMinMargin
		}
		if offsetX > width-dragMinMargin {
			offsetX = width - dragMinMargin
		}
	}
	if offsetY < 0 {
		offsetY = 0
	}
	if offsetY > dragMaxOffsetY {
		offsetY = dragMaxOffsetY
	}

	for {
		if !env.buttonDown() {
			return
		}
		current, ok := env.cursor()
		if !ok {
			return
		}
		if current != cursor {
			cursor = current
			env.move(cursor.x-offsetX, cursor.y-offsetY)
		}
		env.sleep(time.Duration(dragFrameMillis) * time.Millisecond)
	}
}

// startResize resizes the window while the left button is held on an edge. The
// web view covers the whole window, so Windows cannot do this on its own: the
// interface reports the grabbed edge and the window is resized here.
func (w *windowController) startResize(edge string) {
	if w.hwnd == 0 || edge == "" || !w.resizing.CompareAndSwap(false, true) {
		return
	}
	hwnd := w.hwnd
	go func() {
		defer w.resizing.Store(false)
		runResize(edge, resizeEnvironment{
			buttonDown: func() bool {
				pressed, _, _ := procGetAsyncKeyState.Call(vkLeftButton)
				return pressed&0x8000 != 0
			},
			cursor: cursorPosition,
			bounds: func() (winRect, bool) { return windowBounds(hwnd) },
			move: func(left, top, width, height int32) {
				procSetWindowPos.Call(
					hwnd,
					0,
					uintptr(left),
					uintptr(top),
					uintptr(width),
					uintptr(height),
					swpNoZOrder|swpNoActivate,
				)
			},
			sleep: func(duration time.Duration) { time.Sleep(duration) },
		})
	}()
}

// resizeEnvironment collects the platform calls used by the resize loop so the
// logic can be unit tested without a real mouse.
type resizeEnvironment struct {
	buttonDown func() bool
	cursor     func() (winPoint, bool)
	bounds     func() (winRect, bool)
	move       func(left, top, width, height int32)
	sleep      func(time.Duration)
}

// runResize follows the cursor with the grabbed edge until the button is
// released. Every frame is measured from the position where the drag started,
// so rounding errors cannot accumulate.
func runResize(edge string, env resizeEnvironment) {
	start, ok := env.bounds()
	if !ok {
		return
	}
	cursor, ok := env.cursor()
	if !ok {
		return
	}

	for {
		if !env.buttonDown() {
			return
		}
		current, ok := env.cursor()
		if !ok {
			return
		}
		left, top := start.left, start.top
		right, bottom := start.right, start.bottom
		deltaX := current.x - cursor.x
		deltaY := current.y - cursor.y

		if strings.Contains(edge, "left") {
			left += deltaX
		}
		if strings.Contains(edge, "right") {
			right += deltaX
		}
		if strings.Contains(edge, "top") {
			top += deltaY
		}
		if strings.Contains(edge, "bottom") {
			bottom += deltaY
		}

		if right-left < minWindowWidth {
			if strings.Contains(edge, "left") {
				left = right - minWindowWidth
			} else {
				right = left + minWindowWidth
			}
		}
		if bottom-top < minWindowHeight {
			if strings.Contains(edge, "top") {
				top = bottom - minWindowHeight
			} else {
				bottom = top + minWindowHeight
			}
		}

		env.move(left, top, right-left, bottom-top)
		env.sleep(time.Duration(dragFrameMillis) * time.Millisecond)
	}
}

func cursorPosition() (winPoint, bool) {
	var point winPoint
	result, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	return point, result != 0
}

func windowBounds(hwnd uintptr) (winRect, bool) {
	var rect winRect
	result, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
	return rect, result != 0
}
