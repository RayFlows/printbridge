package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/RayFlows/printbridge/server/internal/app"
	"github.com/RayFlows/printbridge/server/internal/config"
	"github.com/RayFlows/printbridge/server/internal/pairing"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	showVersion := flag.Bool("version", false, "print the build version and exit")
	newCode := flag.Bool("new-pairing-code", false, "generate a device pairing code and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("printbridge %s\n", version)
		return 0
	}
	if *newCode {
		code, err := pairing.GenerateCode()
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not generate a pairing code")
			return 1
		}
		fmt.Println(code)
		return 0
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	})).With("service", "PrintBridge", "version", version)

	handler, err := app.NewHandler(cfg, version)
	if err != nil {
		logger.Error("initialization failed", "error", err)
		return 1
	}
	defer handler.Close()
	if cfg.PairingCode == "" {
		logger.Warn("device pairing is disabled; configure PRINTBRIDGE_PAIRING_CODE to enable it")
	}

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.Error("listen failed", "error", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = app.Serve(ctx, listener, handler, cfg.ShutdownTimeout, logger)
	if err != nil {
		logger.Error("server stopped with an error", "error", err)
		return 1
	}
	return 0
}
