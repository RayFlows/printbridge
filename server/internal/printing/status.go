package printing

import (
	"context"
	"slices"

	"github.com/RayFlows/printbridge/server/internal/printers"
)

func sortJobs(jobs []Job) {
	slices.SortFunc(jobs, func(a, b Job) int {
		if (a.State == "queued") != (b.State == "queued") {
			if a.State == "queued" {
				return 1
			}
			return -1
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
}

// List combines queue telemetry with the same reservation order used by Tick.
// It deliberately omits other users' document, user and task identifiers.
func (s *Service) List(ctx context.Context) ([]printers.Printer, error) {
	list, err := s.backend.List(ctx)
	if err != nil {
		return nil, err
	}
	jobs, err := s.repo.ListJobs(ctx)
	if err != nil {
		return nil, ErrBackend
	}
	sortJobs(jobs)
	for i := range list {
		activity := &printers.Activity{State: "idle"}
		for _, job := range jobs {
			if job.Terminal() || job.Settings.PrinterID != list[i].ID {
				continue
			}
			if job.State == "queued" {
				activity.WaitingJobs++
			} else if activity.State == "idle" {
				switch job.State {
				case "printing", "awaiting_flip", "canceling":
					activity.State = job.State
				default:
					activity.State = "submitting"
				}
			}
		}
		list[i].Activity = activity
	}
	return list, nil
}
