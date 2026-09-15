//go:build windows

package main

import (
	"context"
	"errors"
	goruntime "runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

// Windows tray implementation. The system tray icon is owned by a hidden
// message-only window whose thread pumps window messages; tray callbacks
// (icon clicks) arrive as messages on exactly that window. This file starts a
// dedicated OS thread owning that window and running a GetMessage loop for the
// process lifetime, so the icon is independent of Wails' own window lifecycle.

// installTrayIcon creates the tray window + icon and starts its message pump.
func installTrayIcon(ctx context.Context) {
	trayOnce.Do(func() {
		trayCtx = ctx
		go trayLoop()
	})
}

// hideToTray hides the main window but keeps the tray icon and proxy alive,
// mirroring the macOS behavior.
func hideToTray(ctx context.Context) {
	runtime.WindowHide(ctx)
}

// quitRequested reports whether the tray "退出" item asked the app to quit.
// OnBeforeClose consults it so a tray quit is not swallowed as a hide.
func quitRequested() bool {
	return quitFlag.Load()
}

var (
	trayOnce sync.Once
	trayCtx  context.Context
	quitFlag atomic.Bool
)

func showMainWindow() {
	ctx := trayCtx
	if ctx == nil {
		return
	}
	runtime.WindowShow(ctx)
}

func requestQuit() {
	quitFlag.Store(true)
	// Quit the Wails app from a non-main goroutine. OnBeforeClose will be
	// invoked and see quitRequested()==true, letting the real quit proceed.
	if ctx := trayCtx; ctx != nil {
		runtime.Quit(ctx)
	}
}

// ---------------------------------------------------------------------------
// Manual Win32 bindings. The vendored x/sys version does not expose the
// windowing functions the tray needs, so they are bound lazily here.

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procPostMessageW        = user32.NewProc("PostMessageW")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
)

const (
	wmApp = 0x0800 // WM_USER + 0; our tray callback message

	wmLButtonUp   = 0x0202
	wmRButtonUp   = 0x0205
	wmCommand     = 0x0111
	wmContextMenu = 0x007B
	wmDestroy     = 0x0002

	nimAdd    = 0
	nimDelete = 2

	nifMessage = 0x0001
	nifIcon    = 0x0002
	nifTip     = 0x0004

	imageIcon      = 1
	lrDefaultColor = 0x0000
	idiApplication = 32512

	menuOpenCmd = 1
	menuQuitCmd = 2

	tpmRightButton = 0x0002
	tpmNonotify    = 0x0080
	tpmReturnCmd   = 0x0100

	// hwndMessage creates a message-only window.
	hwndMessage = ^uintptr(0) - 2

	appIconResID = 3 // Wails embeds appicon.ico at this resource id
)

type msg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       struct{ x, y int32 }
	lPrivate uint32
}

type point struct{ x, y int32 }

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// notifyIconData mirrors NOTIFYICONDATAW for 64-bit Windows.
type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

var (
	trayHwnd uintptr
	wndProc  uintptr // NewCallback pointers must stay alive
)

func call(proc *windows.LazyProc, args ...uintptr) uintptr {
	r1, _, _ := proc.Call(args...)
	return r1
}

func utf16Ptr(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

// trayLoop owns the tray window thread. It registers a hidden message-only
// window class, creates the window, registers the notification icon, and pumps
// messages until PostQuitMessage stops it.
func trayLoop() {
	goruntime.LockOSThread()
	defer goruntime.UnlockOSThread()

	instance := call(procGetModuleHandleW, 0)
	if instance == 0 {
		return
	}
	if err := registerTrayClass(instance); err != nil {
		return
	}
	trayHwnd = call(procCreateWindowExW,
		0,
		uintptr(unsafe.Pointer(utf16Ptr("AgentRouterTrayWnd"))),
		uintptr(unsafe.Pointer(utf16Ptr(""))),
		0, 0, 0, 0, 0,
		hwndMessage,
		0,
		instance,
		0,
	)
	if trayHwnd == 0 {
		return
	}
	if !addTrayIcon(trayHwnd) {
		return
	}

	var m msg
	for {
		ret := call(procGetMessageW,
			uintptr(unsafe.Pointer(&m)),
			0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			break // WM_QUIT or error
		}
		call(procTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(procDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
	removeTrayIcon(trayHwnd)
}

func registerTrayClass(instance uintptr) error {
	wndProc = windows.NewCallback(trayWndProc)
	wc := &wndClassEx{
		lpfnWndProc:   wndProc,
		hInstance:     instance,
		lpszClassName: utf16Ptr("AgentRouterTrayWnd"),
	}
	wc.cbSize = uint32(unsafe.Sizeof(*wc))
	if call(procRegisterClassExW, uintptr(unsafe.Pointer(wc))) == 0 {
		return errors.New("register AgentRouterTrayWnd class")
	}
	return nil
}

// addTrayIcon registers the notification icon using the embedded app icon
// resource (Wails compiles appicon.ico at resource id 3).
func addTrayIcon(hwnd uintptr) bool {
	instance := call(procGetModuleHandleW, 0)
	hIcon := call(procLoadImageW,
		instance,
		uintptr(appIconResID),
		uintptr(imageIcon),
		16, 16,
		uintptr(lrDefaultColor),
	)
	if hIcon == 0 {
		// Fall back to the shell's default application icon.
		hIcon = call(procLoadImageW, 0, idiApplication, imageIcon, 16, 16, lrDefaultColor)
	}
	nid := &notifyIconData{
		hWnd:             hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmApp,
		hIcon:            hIcon,
	}
	tip := "Agent Router"
	u, _ := windows.UTF16FromString(tip)
	if len(u) > 127 {
		u = u[:127]
	}
	copy(nid.szTip[:], u)
	nid.cbSize = uint32(unsafe.Sizeof(*nid))

	ok := call(procShellNotifyIconW, nimAdd, uintptr(unsafe.Pointer(nid))) != 0
	if hIcon != 0 {
		call(procDestroyIcon, hIcon)
	}
	return ok
}

func removeTrayIcon(hwnd uintptr) {
	nid := &notifyIconData{hWnd: hwnd, uID: 1}
	nid.cbSize = uint32(unsafe.Sizeof(*nid))
	call(procShellNotifyIconW, nimDelete, uintptr(unsafe.Pointer(nid)))
}

// trayWndProc receives the notification icon's callback messages. Left click
// restores the main window; right click opens the context menu.
func trayWndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmApp:
		switch lParam {
		case wmLButtonUp:
			showMainWindow()
		case wmRButtonUp, wmContextMenu:
			showTrayMenu(hwnd)
		}
		return 0
	case wmCommand:
		switch wParam & 0xFFFF {
		case menuOpenCmd:
			showMainWindow()
		case menuQuitCmd:
			requestQuit()
		}
		return 0
	case wmDestroy:
		call(procPostQuitMessage, 0)
		return 0
	}
	return call(procDefWindowProcW, hwnd, uintptr(message), wParam, lParam)
}

// showTrayMenu shows the "打开窗口 / 退出" context menu at the current cursor.
func showTrayMenu(hwnd uintptr) {
	var pt point
	call(procGetCursorPos, uintptr(unsafe.Pointer(&pt)))
	// GetCursorPos returns (true/false); if it fails, still try the window at 0,0.
	// The menu gets activated; posting a WM_NULL after selection closes the
	// shell menus idiomatically.
	call(procSetForegroundWindow, hwnd)

	menu := call(procCreatePopupMenu)
	if menu == 0 {
		return
	}
	defer call(procDestroyMenu, menu)

	call(procAppendMenuW, menu, 0x0000, menuOpenCmd, uintptr(unsafe.Pointer(utf16Ptr("打开窗口"))))
	call(procAppendMenuW, menu, 0x0000, menuQuitCmd, uintptr(unsafe.Pointer(utf16Ptr("退出"))))

	cmd := call(procTrackPopupMenu,
		menu,
		tpmRightButton|tpmNonotify|tpmReturnCmd,
		uintptr(pt.x),
		uintptr(pt.y),
		0,
		hwnd,
		0,
	)
	switch cmd {
	case menuOpenCmd:
		showMainWindow()
	case menuQuitCmd:
		requestQuit()
	}
	call(procPostMessageW, hwnd, 0x0012 /*WM_NULL*/, 0, 0)
}
