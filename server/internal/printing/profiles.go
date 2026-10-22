package printing

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/RayFlows/printbridge/server/internal/printers"
)

// Versioned because a saved job must retain its reload instructions after an upgrade.
const RearFaceUpProfile = "rear-face-up-v1"

type profiledBackend struct {
	Backend
	profiles map[string]string
}

// WithManualDuplexProfiles adds deployment-owned handling information without
// changing CUPS or inferring a paper path from a queue's name or driver.
func WithManualDuplexProfiles(backend Backend, profiles map[string]string) (Backend, error) {
	for queue, profile := range profiles {
		if strings.TrimSpace(queue) == "" || profile != RearFaceUpProfile {
			return nil, fmt.Errorf("PRINTBRIDGE_MANUAL_DUPLEX_PROFILES: unsupported queue or profile")
		}
	}
	return &profiledBackend{Backend: backend, profiles: maps.Clone(profiles)}, nil
}

func (b *profiledBackend) List(ctx context.Context) ([]printers.Printer, error) {
	list, err := b.Backend.List(ctx)
	if err != nil {
		return nil, err
	}
	result := append([]printers.Printer(nil), list...)
	for i := range result {
		result[i].ManualDuplexProfile = b.profiles[result[i].ID]
	}
	return result, nil
}

func resolveManualProfile(settings Settings, printer printers.Printer) (Settings, error) {
	if settings.ManualDuplexProfile == "" {
		return settings, nil // Legacy clients and saved jobs retain explicit calibration.
	}
	if settings.ManualDuplexProfile != RearFaceUpProfile ||
		settings.ManualDuplexProfile != printer.ManualDuplexProfile {
		return Settings{}, ErrSettings
	}
	if !settings.Manual() {
		settings.ManualDuplexProfile = ""
		return settings, nil
	}
	settings.ManualFirstSide = "back"
	settings.BackOrder = "same"
	settings.BackRotation = 0
	// Reload always turns around the physical long edge, even for landscape
	// content. Binding follows the laid-out page, so landscape swaps the axes.
	if settings.Orientation == "landscape" {
		settings.BackRotation = 180
	}
	settings.Calibrated = true
	return settings, nil
}
