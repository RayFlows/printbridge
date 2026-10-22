package canonstatus

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// Run owns the three streams. It forwards print bytes unchanged while consuming
// the backend's status pipe independently, including when print output blocks.
// tailLimit bounds silence after input EOF for schedulers retaining pipe writers
// between documents. Valid reports extend monitoring, so waiting for paper does
// not expire while the device is still reporting it. Expiry never clears a reason.
func Run(ctx context.Context, input io.ReadCloser, output io.WriteCloser, backchannel io.ReadCloser,
	status io.Writer, tailLimit time.Duration) error {
	var outputOnce sync.Once
	closeOutput := func() { outputOnce.Do(func() { _ = output.Close() }) }
	printDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(output, input)
		closeOutput() // The backend needs EOF before it can finish and close FD 3.
		printDone <- err
	}()
	backDone := make(chan struct{})
	activity := make(chan struct{}, 1)
	go func() {
		defer close(backDone)
		if backchannel == nil {
			_, _ = fmt.Fprintln(status, "WARNING: PrintBridge status back-channel unavailable.")
			return
		}
		var decoder Decoder
		last := Unknown
		emit := func(state State) {
			select {
			case activity <- struct{}{}:
			default:
			}
			if state == last {
				return
			}
			last = state
			sign := "-"
			if state == PaperEmpty {
				sign = "+"
			}
			_, _ = fmt.Fprintf(status, "STATE: %smedia-empty-error\n", sign)
		}
		buffer := make([]byte, 4096)
		for {
			n, err := backchannel.Read(buffer)
			decoder.Feed(buffer[:n], emit)
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		_ = input.Close()
		closeOutput()
		if backchannel != nil {
			_ = backchannel.Close()
		}
		<-backDone
	}()
	select {
	case <-ctx.Done():
		_ = input.Close()
		closeOutput()
		<-printDone
		return ctx.Err()
	case err := <-printDone:
		if err != nil {
			return fmt.Errorf("forward print stream: %w", err)
		}
	}
	timer := time.NewTimer(tailLimit)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-backDone:
			return nil
		case <-activity:
			timer.Reset(tailLimit)
		case <-timer.C:
			_, _ = fmt.Fprintln(status, "WARNING: PrintBridge status monitoring ended after input EOF silence.")
			return nil
		}
	}
}
