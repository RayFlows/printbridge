package cups

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"time"
)

// DeviceCanceler invokes an opt-in, root-owned USB helper. The helper separately
// checks CUPS, claims the matching device and verifies fresh idle reports.
type DeviceCanceler struct{ Helper string }

var queueName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,127}$`)

func (d DeviceCanceler) CancelDevice(ctx context.Context, printer string) error {
	if d.Helper == "" || !queueName.MatchString(printer) {
		return errors.New("invalid device cancellation configuration")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// No shell, password, document path or user-supplied command. Helper output
	// is deliberately excluded from logs and public error responses.
	command := exec.CommandContext(ctx, "sudo", "-n", d.Helper, printer)
	if err := command.Run(); err != nil {
		return errors.New("device cancellation could not be verified")
	}
	return nil
}
