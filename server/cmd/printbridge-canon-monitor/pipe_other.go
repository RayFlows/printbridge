//go:build !linux

package main

import (
	"errors"
	"os"
)

func pipeFile(uintptr, string) (*os.File, error) {
	return nil, errors.New("CUPS port monitor requires Linux")
}
