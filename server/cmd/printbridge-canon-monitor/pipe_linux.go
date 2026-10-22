package main

import (
	"errors"
	"os"
	"syscall"
)

func pipeFile(fd uintptr, name string) (*os.File, error) {
	var stat syscall.Stat_t
	if err := syscall.Fstat(int(fd), &stat); err != nil {
		return nil, err
	}
	if fd == 3 && stat.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		// Without an inherited status pipe, FD 3 may already be Go's poller.
		return nil, errors.New("status descriptor is not a pipe")
	}
	// Set nonblocking before NewFile so Go registers inherited CUPS pipes with
	// its poller. Close can then interrupt reads/writes on cancellation.
	if err := syscall.SetNonblock(int(fd), true); err != nil {
		return nil, err
	}
	file := os.NewFile(fd, name)
	if _, err := file.Stat(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
