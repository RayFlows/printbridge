package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/app"
)

func TestShutdownDrainsInFlightRequest(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, "finished")
		case <-r.Context().Done():
		}
	})
	server := startServer(t, handler, 2*time.Second)
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(server.url)
		if err != nil {
			response <- err
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err == nil && string(body) != "finished" {
			err = errors.New("in-flight response was not completed")
		}
		response <- err
	}()
	waitSignal(t, started, "request to start")

	server.cancel()
	select {
	case <-server.exited:
		t.Fatal("server exited before the in-flight request finished")
	case <-time.After(40 * time.Millisecond):
	}
	unblock()

	if err := waitError(t, response, "response"); err != nil {
		t.Fatal(err)
	}
	if err := waitError(t, server.result, "graceful shutdown"); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownDeadlineClosesActiveConnection(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	server := startServer(t, handler, 30*time.Millisecond)
	requestFinished := make(chan struct{})
	go func() {
		defer close(requestFinished)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(server.url)
		if err == nil {
			resp.Body.Close()
		}
	}()
	waitSignal(t, started, "request to start")
	server.cancel()

	if err := waitError(t, server.result, "forced shutdown"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want a shutdown deadline error", err)
	}
	waitSignal(t, cancelled, "request context cancellation")
	waitSignal(t, requestFinished, "client connection closure")
}

func TestListenerFailureIsReturned(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	err = app.Serve(context.Background(), listener, http.NotFoundHandler(), time.Second, logger)
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("error = %v, want a listener failure", err)
	}
}

type runningServer struct {
	url    string
	cancel context.CancelFunc
	result <-chan error
	exited <-chan struct{}
}

func startServer(t *testing.T, handler http.Handler, timeout time.Duration) runningServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	exited := make(chan struct{})
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	go func() {
		defer close(exited)
		result <- app.Serve(ctx, listener, handler, timeout, logger)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			listener.Close()
			t.Error("server did not exit during cleanup")
		}
	})
	return runningServer{
		url:    "http://" + listener.Addr().String(),
		cancel: cancel,
		result: result,
		exited: exited,
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
	}
}

func waitError(t *testing.T, result <-chan error, operation string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
		return nil
	}
}
