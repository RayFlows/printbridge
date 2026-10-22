package canonstatus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type lines struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lines) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(b)
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMonitorDuringBackpressureAndAfterInputEOF(t *testing.T) {
	in, source := io.Pipe()
	sink, out := io.Pipe()
	back, device := io.Pipe()
	var log lines
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- Run(ctx, in, out, back, &log, time.Second) }()
	payload := bytes.Repeat([]byte{0, 255, 1, 2, 3}, 50000)
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_, _ = source.Write(payload)
		_ = source.Close()
	}()
	_, _ = io.WriteString(device, statusXML("stopped", "MediaEmpty"))
	waitFor(t, func() bool { return strings.Contains(log.String(), "STATE: +media-empty-error\n") })
	// No output consumer yet: status processing must run independently of print writes.
	_, _ = io.WriteString(device, statusXML("stopped", "MediaEmpty"))
	received, err := io.ReadAll(sink)
	if err != nil || !bytes.Equal(payload, received) {
		t.Fatalf("print bytes changed: %d, %v", len(received), err)
	}
	<-sent
	select {
	case err := <-done:
		t.Fatalf("monitor stopped before backend EOF: %v", err)
	default:
	}
	_, _ = io.WriteString(device, statusXML("idle", ""))
	waitFor(t, func() bool { return strings.Contains(log.String(), "STATE: -media-empty-error\n") })
	_ = device.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if log.String() != "STATE: +media-empty-error\nSTATE: -media-empty-error\n" {
		t.Fatalf("duplicate or unexpected status: %q", log.String())
	}
}

func TestMonitorCancellationClosesBlockedIO(t *testing.T) {
	for _, blockedOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "input", true: "output"}[blockedOutput], func(t *testing.T) {
			in, source := io.Pipe()
			sink, out := io.Pipe()
			back, device := io.Pipe()
			defer source.Close()
			defer sink.Close()
			defer device.Close()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			var log lines
			go func() { done <- Run(ctx, in, out, back, &log, time.Second) }()
			if blockedOutput {
				_, _ = source.Write([]byte("unread output"))
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancel did not interrupt blocked IO")
			}
		})
	}
}

func TestAbsentBackchannelDoesNotLosePrintData(t *testing.T) {
	sink, out := io.Pipe()
	done := make(chan error, 1)
	var log lines
	go func() {
		done <- Run(context.Background(), io.NopCloser(strings.NewReader("print data")), out, nil, &log, time.Second)
	}()
	got, _ := io.ReadAll(sink)
	if err := <-done; err != nil || string(got) != "print data" {
		t.Fatalf("%q %v", got, err)
	}
	if strings.Contains(log.String(), "STATE:") {
		t.Fatal("invented a device state")
	}
}

func TestTailTimeoutDoesNotClearUnresolvedShortage(t *testing.T) {
	in, source := io.Pipe()
	sink, out := io.Pipe()
	back, device := io.Pipe()
	defer device.Close()
	var log lines
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), in, out, back, &log, 30*time.Millisecond) }()
	_, _ = io.WriteString(device, statusXML("stopped", "MediaEmpty"))
	waitFor(t, func() bool { return strings.Contains(log.String(), "+media-empty-error") })
	_ = source.Close()
	_, _ = io.ReadAll(sink)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "-media-empty") {
		t.Fatal("timeout cleared unresolved shortage")
	}
}

func TestPaperOutReportsExtendMonitoringAfterInputEOF(t *testing.T) {
	in, source := io.Pipe()
	sink, out := io.Pipe()
	back, device := io.Pipe()
	defer device.Close()
	var log lines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, in, out, back, &log, 300*time.Millisecond) }()
	_ = source.Close()
	_, _ = io.ReadAll(sink)
	for range 15 {
		_, _ = io.WriteString(device, statusXML("stopped", "MediaEmpty"))
		time.Sleep(30 * time.Millisecond)
		select {
		case err := <-done:
			t.Fatalf("ongoing paper-out reports must keep monitoring alive: %v", err)
		default:
		}
	}
	_, _ = io.WriteString(device, statusXML("processing", ""))
	_ = device.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if log.String() != "STATE: +media-empty-error\nSTATE: -media-empty-error\n" {
		t.Fatal(log.String())
	}
}
