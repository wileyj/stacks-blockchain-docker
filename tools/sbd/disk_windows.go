//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// freeSpace returns the bytes available to this user on path's volume, and the volume as its id.
func freeSpace(path string) (uint64, string, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, "", err
	}
	var avail uint64
	if r, _, err := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&avail)), 0, 0); r == 0 {
		return 0, "", err
	}
	return avail, strings.ToLower(filepath.VolumeName(path)), nil
}
