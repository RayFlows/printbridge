package management

import (
	"context"
	"slices"
	"time"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/printers"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

type JobRepository interface {
	ListJobs(context.Context) ([]printing.Job, error)
	GetJob(context.Context, string) (printing.Job, error)
}
type PrinterReader interface {
	List(context.Context) ([]printers.Printer, error)
}
type Canceller interface {
	Cancel(context.Context, string, string) (printing.Job, error)
	ConfirmCancel(context.Context, string, string, bool) (printing.Job, error)
}

type Task struct {
	ID                   string    `json:"id"`
	UserID               string    `json:"userId"`
	UserName             string    `json:"userName"`
	PrinterID            string    `json:"printerId"`
	State                string    `json:"state"`
	CUPSState            int       `json:"cupsJobState,omitempty"`
	SheetCount           int       `json:"sheetCount"`
	CreatedAt            time.Time `json:"createdAt"`
	Problem              string    `json:"problem,omitempty"`
	DeviceCancelRequired bool      `json:"deviceCancelRequired,omitempty"`
}
type Dashboard struct {
	Printers               []printers.Printer `json:"printers"`
	PrinterStatusAvailable bool               `json:"printerStatusAvailable"`
	Tasks                  []Task             `json:"tasks"`
	Settings               accounts.Settings  `json:"settings"`
}
type Service struct {
	users    *accounts.Service
	jobs     JobRepository
	printers PrinterReader
	prints   Canceller
}

func New(users *accounts.Service, jobs JobRepository, printers PrinterReader, prints Canceller) *Service {
	return &Service{users: users, jobs: jobs, printers: printers, prints: prints}
}
func (s *Service) Dashboard(ctx context.Context, actor string) (Dashboard, error) {
	if err := s.users.RequireAdmin(ctx, actor); err != nil {
		return Dashboard{}, err
	}
	users, err := s.users.Users(ctx, actor)
	if err != nil {
		return Dashboard{}, err
	}
	names := map[string]string{}
	for _, user := range users {
		names[user.ID] = user.Name
	}
	jobs, err := s.jobs.ListJobs(ctx)
	if err != nil {
		return Dashboard{}, err
	}
	result := Dashboard{Tasks: []Task{}}
	for _, job := range jobs {
		if !job.Terminal() {
			result.Tasks = append(result.Tasks, Task{ID: job.ID, UserID: job.Owner, UserName: names[job.Owner],
				PrinterID: job.Settings.PrinterID, State: job.State, CUPSState: job.CUPSState,
				SheetCount: job.SheetCount, CreatedAt: job.CreatedAt,
				Problem: job.Problem, DeviceCancelRequired: job.DeviceCancelRequired})
		}
	}
	slices.SortFunc(result.Tasks, func(a, b Task) int { return a.CreatedAt.Compare(b.CreatedAt) })
	result.Printers, err = s.printers.List(ctx)
	if err != nil {
		result.Printers = []printers.Printer{}
	} else {
		result.PrinterStatusAvailable = true
	}
	if result.Printers == nil {
		result.Printers = []printers.Printer{}
	}
	result.Settings, err = s.users.Settings(ctx)
	return result, err
}
func (s *Service) Cancel(ctx context.Context, actor, id string) (printing.Job, error) {
	if err := s.users.RequireAdmin(ctx, actor); err != nil {
		return printing.Job{}, err
	}
	job, err := s.jobs.GetJob(ctx, id)
	if err != nil {
		return printing.Job{}, err
	}
	return s.prints.Cancel(ctx, job.Owner, id)
}

func (s *Service) ConfirmCancel(ctx context.Context, actor, id string, deviceStopped bool) (printing.Job, error) {
	if err := s.users.RequireAdmin(ctx, actor); err != nil {
		return printing.Job{}, err
	}
	job, err := s.jobs.GetJob(ctx, id)
	if err != nil {
		return printing.Job{}, err
	}
	return s.prints.ConfirmCancel(ctx, job.Owner, id, deviceStopped)
}
