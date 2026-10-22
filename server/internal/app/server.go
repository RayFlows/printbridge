package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

func Serve(ctx context.Context, listener net.Listener, handler http.Handler, shutdownTimeout time.Duration, logger *slog.Logger) error {
	defer listener.Close()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	defer server.Close()
	stopped := make(chan error, 1)
	go func() {
		stopped <- server.Serve(listener)
	}()
	logger.Info("HTTP server started", "address", listener.Addr().String())

	select {
	case err := <-stopped:
		return serveError(err)
	case <-ctx.Done():
		logger.Info("HTTP server shutting down")
	}

	// The process context is already cancelled; draining needs its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		<-stopped
		return fmt.Errorf("HTTP shutdown: %w", err)
	}
	if err := serveError(<-stopped); err != nil {
		return err
	}
	logger.Info("HTTP server stopped")
	return nil
}

func serveError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
