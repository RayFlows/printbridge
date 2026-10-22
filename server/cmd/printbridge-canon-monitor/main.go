package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RayFlows/printbridge/server/internal/canonstatus"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("printbridge-canon-monitor 1")
		return
	}
	if len(os.Args) != 6 && len(os.Args) != 7 {
		fmt.Fprintln(os.Stderr, "ERROR: This program is a CUPS port monitor.")
		os.Exit(1)
	}
	input, err := pipeFile(0, "print input")
	if err == nil && len(os.Args) == 7 {
		_ = input.Close()
		input, err = os.Open(os.Args[6])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: Cannot open print input.")
		os.Exit(1)
	}
	output, err := pipeFile(1, "print output")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: Cannot open print output.")
		os.Exit(1)
	}
	var backchannel io.ReadCloser
	if file, err := pipeFile(3, "status back-channel"); err == nil {
		backchannel = file
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := canonstatus.Run(ctx, input, output, backchannel, os.Stderr, time.Minute); err != nil &&
		!errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "ERROR: PrintBridge port monitor could not forward the print stream.")
		os.Exit(1)
	}
}
