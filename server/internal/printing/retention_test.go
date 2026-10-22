package printing_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

type documentProcessor struct{ *processor }

func (p documentProcessor) Prepare(_ context.Context, source, _ string, output string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(output, data, 0600)
}
func (p documentProcessor) Inspect(context.Context, string) (int, error) { return 3, nil }

func TestCleanupCollectsSourcePreviewsAndPlansAfterManualCompletion(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	docRoot := filepath.Join(t.TempDir(), "documents")
	docs, err := documents.New(docRoot, f.db, documentProcessor{f.processor}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := docs.Create(ctx, "owner", "synthetic.pdf", strings.NewReader("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Preview(ctx, "owner", doc.ID, 1); err != nil {
		t.Fatal(err)
	}
	f.service, err = printing.New(f.root, f.db, docs, f.processor, f.backend)
	if err != nil {
		t.Fatal(err)
	}
	options := settings()
	options.Sides, options.Calibrated = "manual-long-edge", true
	plan, err := f.service.Prepare(ctx, "owner", doc.ID, options, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Preview(ctx, "owner", plan.ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	job := f.create(t, plan, "request-0000000001")
	f.service.Tick(ctx)
	f.backend.complete(1)
	f.service.Tick(ctx)
	if err := f.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(docRoot, doc.ID, "original"),
		filepath.Join(docRoot, doc.ID, "source.pdf"), filepath.Join(docRoot, doc.ID, "page-1.png"),
		filepath.Join(f.root, "plans", plan.ID, "layout.pdf"), filepath.Join(f.root, "jobs", job.ID, "back.pdf")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("awaiting flip lost content", path, err)
		}
	}
	if _, err := f.service.Continue(ctx, "owner", job.ID, true); err != nil {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	f.backend.complete(2)
	f.service.Tick(ctx)
	if err := f.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(docRoot, doc.ID), filepath.Join(f.root, "plans", plan.ID),
		filepath.Join(f.root, "jobs", job.ID)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("completed job retained content", path, err)
		}
	}
	if _, err := docs.Get(ctx, "owner", doc.ID); !errors.Is(err, documents.ErrNotFound) {
		t.Fatal("source still accessible", err)
	}
	if current := f.job(t, job.ID); current.State != "completed" {
		t.Fatal("cleanup removed diagnostic metadata", current)
	}
}

func TestActiveTaskRestrictionIsScopedToSessionAndPrinter(t *testing.T) {
	for _, other := range []string{"session", "printer"} {
		t.Run(other, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			plan := f.plan(t, false)
			existing := printing.Job{ID: strings.Repeat("e", 32), PlanID: plan.ID, Owner: "owner",
				Key: "request-0000000000", Settings: plan.Settings, State: "printing", Phase: "single",
				CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if other == "session" {
				existing.Owner = "other-owner"
			} else {
				existing.Settings.PrinterID = "other-printer"
			}
			if err := f.db.InsertJob(ctx, existing); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.Create(ctx, "owner", plan.ID, "request-0000000001"); err != nil {
				t.Fatal("unrelated active task blocked submission", err)
			}
		})
	}
}

func TestOneActiveTaskPerSessionAndPrinter(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan := f.plan(t, true)
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes, conflicts := 0, 0
	for _, key := range []string{"request-0000000001", "request-0000000002"} {
		wg.Go(func() {
			_, err := f.service.Create(ctx, "owner", plan.ID, key)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, printing.ErrActiveJob):
				conflicts++
			default:
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if successes != 1 || conflicts != 1 || f.backend.creates != 0 {
		t.Fatal("concurrent submissions did not preserve a single pending job", successes, conflicts)
	}
	jobs, err := f.service.Jobs(ctx, "owner", true)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	for _, state := range []string{"printing", "awaiting_flip", "canceling"} {
		job := jobs[0]
		job.State = state
		if err := f.db.SaveJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.Create(ctx, "owner", plan.ID, "another-request-00001"); !errors.Is(err, printing.ErrActiveJob) {
			t.Fatal("unfinished state allowed another task", state, err)
		}
	}
	if got, err := f.service.Jobs(ctx, "another-owner", true); err != nil || len(got) != 0 {
		t.Fatal("another session saw this job", got, err)
	}
}

func TestFinishedFilesAreRemovedBeforeMetadataAndIdempotency(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan := f.plan(t, false)
	job := f.create(t, plan, "request-0000000001")
	if _, err := f.service.Cancel(ctx, "owner", job.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Join(f.root, "jobs", job.ID), filepath.Join(f.root, "plans", plan.ID)} {
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("finished print content retained", directory, err)
		}
	}
	previous, err := f.service.Create(ctx, "owner", plan.ID, "request-0000000001")
	if err != nil || previous.ID != job.ID || previous.State != "canceled" {
		t.Fatal("cleanup broke idempotency", previous, err)
	}
	active, _ := f.service.Jobs(ctx, "owner", true)
	history, _ := f.service.Jobs(ctx, "owner", false)
	if len(active) != 0 || len(history) != 1 {
		t.Fatal("terminal job appeared as active", active, history)
	}
	previous.UpdatedAt = time.Now().Add(-72 * time.Hour)
	if err := f.db.SaveJob(ctx, previous); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Job(ctx, "owner", job.ID); !errors.Is(err, printing.ErrNotFound) {
		t.Fatal("three-day task metadata was retained", err)
	}
}

func TestExpiredJobsCancelWithoutSendingAnotherPage(t *testing.T) {
	for _, state := range []string{"queued", "awaiting_flip", "printing", "unknown submission", "unresolvable submission"} {
		t.Run(state, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			job := f.create(t, f.plan(t, true), "request-0000000001")
			if state != "queued" {
				f.backend.createError = state == "unknown submission" || state == "unresolvable submission"
				f.service.Tick(ctx)
			}
			if state == "unresolvable submission" {
				f.backend.jobs = map[int]printing.Status{}
			}
			job = f.job(t, job.ID)
			if state == "awaiting_flip" {
				f.backend.complete(1)
				f.service.Tick(ctx)
				job = f.job(t, job.ID)
			}
			job.CreatedAt = time.Now().Add(-73 * time.Hour)
			// Polling keeps UpdatedAt recent; the deadline must use CreatedAt.
			job.UpdatedAt = time.Now()
			if err := f.db.SaveJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			creates, sends, releases := f.backend.creates, f.backend.sends, f.backend.releases
			if _, err := f.service.Continue(ctx, "owner", job.ID, true); !errors.Is(err, printing.ErrConflict) {
				t.Fatal("expired job continued", err)
			}
			for i := 0; i < 3; i++ {
				f.service.Tick(ctx)
			}
			if f.job(t, job.ID).DeviceCancelRequired {
				if _, err := f.service.ConfirmCancel(ctx, "owner", job.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.service.Cleanup(ctx); err != nil {
				t.Fatal(err)
			}
			current := f.job(t, job.ID)
			if state == "unresolvable submission" {
				if current.State != "canceling" {
					t.Fatal("unknown job was forgotten", current)
				}
				if _, err := os.Stat(filepath.Join(f.root, "jobs", job.ID)); err != nil {
					t.Fatal("unconfirmed cancellation lost its files", err)
				}
			} else if current.State != "canceled" {
				t.Fatal("expiration not canceled", current)
			}
			if creates != f.backend.creates || sends != f.backend.sends || releases != f.backend.releases {
				t.Fatal("expiration resubmitted print data")
			}
		})
	}
}

func TestPaperInterruptionRetainsTaskWithoutResubmissionOrPrematureFlip(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	job := f.create(t, f.plan(t, true), "request-0000000001")
	f.service.Tick(ctx)
	status := f.backend.jobs[1]
	status.State = 6
	f.backend.jobs[1] = status
	for i := 0; i < 3; i++ {
		f.service.Tick(ctx)
	}
	current := f.job(t, job.ID)
	if current.State != "printing" || current.Problem != "printer_stopped" {
		t.Fatal("stopped printer advanced to paper reload", current)
	}
	if _, err := f.service.Continue(ctx, "owner", job.ID, true); !errors.Is(err, printing.ErrConflict) {
		t.Fatal("paper recovery was mistaken for the second pass", err)
	}
	status.State = 5
	f.backend.jobs[1] = status
	f.service.Tick(ctx)
	if f.job(t, job.ID).Problem != "" {
		t.Fatal("recovered printer retained a stopped warning")
	}
	f.backend.complete(1)
	f.service.Tick(ctx)
	if f.job(t, job.ID).State != "awaiting_flip" || f.backend.creates != 1 || f.backend.sends != 1 || f.backend.releases != 1 {
		t.Fatal("recovery duplicated the current pass")
	}
}
