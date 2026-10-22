package printing_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/printing"
)

func TestSharedPrinterActivityDoesNotExposeAnotherOwnersTask(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plan := f.plan(t, false)
	active := f.create(t, plan, "request-0000000001")
	f.service.Tick(ctx)
	next := printing.Job{ID: strings.Repeat("e", 32), PlanID: plan.ID, Owner: "another-owner",
		Name: "private-name.pdf", Key: "request-0000000002", Settings: plan.Settings,
		State: "queued", Phase: "single", CreatedAt: active.CreatedAt.Add(-time.Second)}
	if err := f.db.InsertJob(ctx, next); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"printing", "awaiting_flip", "canceling"} {
		active.State = state
		if err := f.db.SaveJob(ctx, active); err != nil {
			t.Fatal(err)
		}
		list, err := f.service.List(ctx)
		if err != nil || len(list) != 1 || list[0].Activity == nil {
			t.Fatal("shared activity missing", list, err)
		}
		if list[0].Activity.State != state || list[0].Activity.WaitingJobs != 1 {
			t.Fatal("backlog displaced the printer reservation", list[0].Activity)
		}
		if state == "printing" {
			f.service.Tick(ctx)
			waiting, err := f.db.GetJob(ctx, next.ID)
			if err != nil || waiting.State != "queued" || waiting.CUPSID != 0 {
				t.Fatal("another owner's waiting job was submitted during the reservation", waiting, err)
			}
		}
		data, _ := json.Marshal(list)
		for _, private := range []string{active.ID, next.ID, active.Owner, next.Owner, next.Name} {
			if strings.Contains(string(data), private) {
				t.Fatal("shared printer activity exposed task data", private)
			}
		}
	}
	next.State = "canceled"
	active.State = "completed"
	_ = f.db.SaveJob(ctx, next)
	_ = f.db.SaveJob(ctx, active)
	list, err := f.service.List(ctx)
	if err != nil || list[0].Activity.State != "idle" || list[0].Activity.WaitingJobs != 0 {
		t.Fatal("finished jobs still occupy the printer", list, err)
	}
}

func TestJobCUPSSnapshotDistinguishesPendingFromProcessing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	job := f.create(t, f.plan(t, false), "request-0000000001")
	f.service.Tick(ctx)
	if got := f.job(t, job.ID); got.CUPSState != 0 {
		t.Fatal("held snapshot remained current after release", got.CUPSState)
	}
	for _, state := range []int{3, 5, 6} {
		status := f.backend.jobs[1]
		status.State = state
		f.backend.jobs[1] = status
		f.service.Tick(ctx)
		if got := f.job(t, job.ID); got.CUPSState != state || got.State != "printing" {
			t.Fatal("job's own IPP state was not persisted", got)
		}
	}
	status := f.backend.jobs[1]
	status.Name = "different-job"
	status.State = 9
	f.backend.jobs[1] = status
	f.service.Tick(ctx)
	if got := f.job(t, job.ID); got.CUPSState != 6 {
		t.Fatal("mismatching CUPS job was trusted", got)
	}
}
