//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmTrayIcon     = 0x0400 + 1
	wmRButtonUp    = 0x0205
	wmLDblClick    = 0x0203
	wmCommand      = 0x0111
	wmDestroy      = 0x0002
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	nimAdd         = 0
	nimDelete      = 2
	nifMessage     = 0x00000001
	nifIcon        = 0x00000002
	nifTip         = 0x00000004
	mfString       = 0x00000000
	tpmReturnCmd   = 0x0100
	swHide         = 0
	imageIcon      = 1
	lrLoadFromFile = 0x00000010
	menuOpen       = 1
	menuExit       = 2
)

var (
	trayUser32  = windows.NewLazySystemDLL("user32.dll")
	trayKernel  = windows.NewLazySystemDLL("kernel32.dll")
	trayShell32 = windows.NewLazySystemDLL("shell32.dll")

	procRegisterTrayClass = trayUser32.NewProc("RegisterClassExW")
	procCreateTrayWindow  = trayUser32.NewProc("CreateWindowExW")
	procDefTrayWindow     = trayUser32.NewProc("DefWindowProcW")
	procGetMessage        = trayUser32.NewProc("GetMessageW")
	procTranslateMessage  = trayUser32.NewProc("TranslateMessage")
	procDispatchMessage   = trayUser32.NewProc("DispatchMessageW")
	procPostQuitMessage   = trayUser32.NewProc("PostQuitMessage")
	procShellNotifyIcon   = trayShell32.NewProc("Shell_NotifyIconW")
	procLoadTrayImage     = trayUser32.NewProc("LoadImageW")
	procCreatePopupMenu   = trayUser32.NewProc("CreatePopupMenu")
	procAppendMenu        = trayUser32.NewProc("AppendMenuW")
	procTrackPopupMenu    = trayUser32.NewProc("TrackPopupMenu")
	procDestroyMenu       = trayUser32.NewProc("DestroyMenu")
	procGetConsoleWindow  = trayKernel.NewProc("GetConsoleWindow")
	procShowConsole       = trayUser32.NewProc("ShowWindow")
	procTrayMessageBox    = trayUser32.NewProc("MessageBoxW")
)

type trayClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  uintptr
	LpszClassName uintptr
	HIconSm       uintptr
}

type trayPoint struct {
	X int32
	Y int32
}

type trayMsg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       trayPoint
	LPrivate uint32
}

type trayGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type notifyIconData struct {
	CbSize       uint32
	HWnd         uintptr
	UID          uint32
	UFlags       uint32
	UCallbackMsg uint32
	HIcon        uintptr
	SzTip        [128]uint16
	DwState      uint32
	DwStateMask  uint32
	SzInfo       [256]uint16
	UTimeout     uint32
	SzInfoTitle  [64]uint16
	DwInfoFlags  uint32
	GuidItem     trayGUID
	HBalloonIcon uintptr
}

var (
	trayHwnd    windows.HWND
	trayIcon    uintptr
	trayPort    int
	trayName    string
	trayProcPtr uintptr
)

func trayUTF16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

func copyTrayText(dst []uint16, s string) {
	p := trayUTF16(s)
	if p == nil {
		return
	}
	src := unsafe.Slice(p, len(s)+1)
	copy(dst, src)
	if len(dst) > 0 {
		dst[len(dst)-1] = 0
	}
}

// runTray starts the HTTP server and keeps a hidden message window alive for
// the system-tray icon. The icon uses favicon.ico, which is the netdisk logo.
// trayErrorBox shows a modal message so startup failures are visible even
// though the console window is hidden in tray mode.
func trayErrorBox(text string) {
	title := trayUTF16("老杨网盘启动失败")
	body := trayUTF16(text)
	if title == nil || body == nil {
		return
	}
	// MB_OK | MB_ICONERROR | MB_SETFOREGROUND
	procTrayMessageBox.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), 0x00000010|0x00010000)
}

func runTray(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		trayErrorBox("无法读取配置文件：" + configPath + "\n\n" + err.Error() + "\n\n请先双击 install.bat 完成安装。")
		return err
	}
	trayPort = cfg.Port
	trayName = cfg.Name
	if clean := sanitizeSiteName(cfg.Name); clean != "" {
		trayName = clean
	}
	hConsole, _, _ := procGetConsoleWindow.Call()
	if hConsole != 0 {
		procShowConsole.Call(hConsole, swHide)
	}
	go func() {
		if err := run(configPath); err != nil {
			log.Printf("server error: %v", err)
			trayErrorBox("网盘服务启动失败：\n\n" + err.Error())
		}
	}()
	runTrayMessageLoop()
	return nil
}

func extractTrayIcon() (string, error) {
	b, err := webFS.ReadFile("web/favicon.ico")
	if err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), "lywp-tray.ico")
	if err := os.WriteFile(path, b, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func addTrayIcon() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tray icon unavailable: %v", r)
		}
	}()
	iconPath, err := extractTrayIcon()
	if err != nil {
		log.Printf("tray icon extract failed: %v", err)
		return
	}
	defer os.Remove(iconPath)
	name := trayUTF16(iconPath)
	if name == nil {
		return
	}
	r, _, _ := procLoadTrayImage.Call(0, uintptr(unsafe.Pointer(name)), imageIcon, 0, 0, lrLoadFromFile)
	if r == 0 {
		log.Printf("tray icon load failed")
		return
	}
	trayIcon = r
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = uintptr(trayHwnd)
	nid.UID = 1
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMsg = wmTrayIcon
	nid.HIcon = trayIcon
	copyTrayText(nid.SzTip[:], "老杨网盘")
	procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
}

func removeTrayIcon() {
	if trayHwnd == 0 {
		return
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = uintptr(trayHwnd)
	nid.UID = 1
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func openNetdisk() {
	url := "http://localhost:" + itoa(trayPort)
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func showTrayMenu(x, y int32) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)
	procAppendMenu.Call(hMenu, mfString, menuOpen, uintptr(unsafe.Pointer(trayUTF16("打开网盘"))))
	procAppendMenu.Call(hMenu, mfString, menuExit, uintptr(unsafe.Pointer(trayUTF16("退出"))))
	cmd, _, _ := procTrackPopupMenu.Call(hMenu, tpmReturnCmd, uintptr(x), uintptr(y), 0, uintptr(trayHwnd), 0)
	switch cmd {
	case menuOpen:
		openNetdisk()
	case menuExit:
		removeTrayIcon()
		close(trayStop)
		procPostQuitMessage.Call(0)
	}
}

func trayWndProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmTrayIcon:
		switch lParam {
		case wmRButtonUp:
			showTrayMenuAtCursor()
		case wmLDblClick:
			openNetdisk()
		}
		return 0
	case wmCommand:
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefTrayWindow.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}

func showTrayMenuAtCursor() {
	procGetCursorPos := trayUser32.NewProc("GetCursorPos")
	var pt trayPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	showTrayMenu(pt.X, pt.Y)
}

func runTrayMessageLoop() {
	trayProcPtr = windows.NewCallback(trayWndProc)
	instance, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	className := trayUTF16("LYWPTrayWindow")
	wc := trayClassEx{
		CbSize:        uint32(unsafe.Sizeof(trayClassEx{})),
		LpfnWndProc:   trayProcPtr,
		HInstance:     instance,
		LpszClassName: uintptr(unsafe.Pointer(className)),
	}
	if r, _, _ := procRegisterTrayClass.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		log.Printf("tray window class registration failed")
		return
	}
	messageOnly := ^uintptr(0) - 2
	r, _, _ := procCreateTrayWindow.Call(
		0, uintptr(unsafe.Pointer(className)), 0, wsChild|wsVisible,
		0, 0, 0, 0, messageOnly, 0, instance, 0,
	)
	if r == 0 {
		log.Printf("tray window creation failed")
		return
	}
	trayHwnd = windows.HWND(r)
	addTrayIcon()
	var m trayMsg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	removeTrayIcon()
}
