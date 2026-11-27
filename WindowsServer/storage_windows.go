//go:build windows

package main

import (
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StorageInfo is returned by the storage API.
type StorageInfo struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
	Used  uint64 `json:"used"`
}

// defaultConfigPath keeps the Windows install entirely in the user profile,
// so no administrator rights are needed to change settings or skins.
func defaultConfigPath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.Getenv("APPDATA")
	}
	if base == "" {
		base = "."
	}
	return filepath.Join(base, "LYWP", "config.json")
}

// storageInfo reports real free space for the configured netdisk root.
func storageInfo(path string) StorageInfo {
	var out StorageInfo
	if path == "" {
		return out
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return out
	}
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	var available, total, totalFree uint64
	r, _, _ := proc.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&available)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return out
	}
	out.Total = total
	out.Free = available
	out.Used = total - totalFree
	return out
}
