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
	free, _, id, err := diskSpace(path)
	return free, id, err
}

// diskSpace returns the bytes available to this user and the volume's size, and the volume as its id.
func diskSpace(path string) (free, total uint64, id string, err error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, "", err
	}
	if r, _, err := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), 0); r == 0 {
		return 0, 0, "", err
	}
	return free, total, strings.ToLower(filepath.VolumeName(path)), nil
}
