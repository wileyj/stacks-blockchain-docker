//go:build unix

package main

import (
	"fmt"
	"syscall"
)

// freeSpace returns the bytes available to this user on path's filesystem, and an id for it.
func freeSpace(path string) (uint64, string, error) {
	free, _, id, err := diskSpace(path)
	return free, id, err
}

// diskSpace returns the bytes available to this user and the filesystem's size, and an id for it.
func diskSpace(path string) (free, total uint64, id string, err error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, "", err
	}
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, 0, "", err
	}
	return uint64(fs.Bavail) * uint64(fs.Bsize), uint64(fs.Blocks) * uint64(fs.Bsize), fmt.Sprint(st.Dev), nil
}
