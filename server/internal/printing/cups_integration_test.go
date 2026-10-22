package printing_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/cups"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

// Invoked only by deploy/verify-canon-monitor.py against its private scheduler.
// Its ServerBin contains the synthetic bridgefake backend and no USB backend.
func TestCancellationAgainstIsolatedCUPS(t *testing.T) {
	root := os.Getenv("PRINTBRIDGE_TEST_CUPS_ROOT")
	if root == "" {
		t.Skip("requires the isolated CUPS harness")
	}
	endpoint := os.Getenv("PRINTBRIDGE_TEST_CUPS_URL")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" ||
		!strings.HasPrefix(filepath.Base(root), "printbridge-virtual-cups-") {
		t.Fatal("refusing a scheduler outside the isolated harness")
	}
	config, err := os.ReadFile(filepath.Join(root, "cups-files.conf"))
	if err != nil || !strings.Contains(string(config), "ServerBin "+filepath.Join(root, "bin")+"\n") {
		t.Fatal("isolated ServerBin was not verified", err)
	}
	backends, err := os.ReadDir(filepath.Join(root, "bin", "backend"))
	if err != nil || len(backends) != 1 || backends[0].Name() != "bridgefake" {
		t.Fatal("isolated scheduler must contain only the synthetic backend", err)
	}
	client, err := cups.New(endpoint, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f := setup(t)
	f.service, err = printing.New(f.root, f.db, source{}, f.processor, client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	device := &deviceCanceler{err: errors.New("synthetic device has not cleared its buffer")}
	f.service.WithDeviceCanceler(device)
	marker := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker("delay-cancel")
	settings := settings()
	settings.Media, settings.ColorMode = "", ""
	plan, err := f.service.Prepare(ctx, "owner", "doc", settings, "")
	if err != nil {
		t.Fatal(err)
	}
	first := f.create(t, plan, "request-0000000001")
	f.service.Tick(ctx)
	first = f.job(t, first.ID)
	if first.State != "printing" || first.CUPSID == 0 {
		t.Fatal("synthetic task did not reach CUPS", first.State, first.Problem)
	}
	waitSynthetic(t, func() bool {
		_, err := os.Stat(filepath.Join(root, "received"))
		return err == nil
	})
	if _, err := f.service.Cancel(ctx, "owner", first.ID); err != nil {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	waitSynthetic(t, func() bool {
		status, err := client.Status(ctx, first.CUPSID)
		return err == nil && status.State == 7
	})
	f.service.Tick(ctx)
	if got := f.job(t, first.ID); got.State != "canceling" || got.Problem != "printer_busy" {
		t.Fatal("terminal CUPS job with a live backend released the barrier", got.State, got.Problem)
	}
	if _, err := f.service.ConfirmCancel(ctx, "owner", first.ID, true); !errors.Is(err, printing.ErrConflict) {
		t.Fatal("live backend accepted device confirmation", err)
	}
	// Keep a later owner's task queued while the canceled synthetic backend lives.
	next := printing.Job{ID: strings.Repeat("f", 32), PlanID: plan.ID, Owner: "other",
		Key: "request-0000000002", Name: plan.Name, Settings: plan.Settings, SheetCount: plan.SheetCount,
		State: "queued", Phase: "single", CreatedAt: first.CreatedAt.Add(-time.Second), UpdatedAt: time.Now()}
	if err := f.db.InsertJob(ctx, next); err != nil {
		t.Fatal(err)
	}
	nextDir := filepath.Join(f.root, "jobs", next.ID)
	if err := os.Mkdir(nextDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nextDir, "single.pdf"), []byte("synthetic-next"), 0600); err != nil {
		t.Fatal(err)
	}
	f.service, err = printing.New(f.root, f.db, source{}, f.processor, client)
	if err != nil {
		t.Fatal(err)
	}
	f.service.WithDeviceCanceler(device)
	for i := 0; i < 3; i++ {
		f.service.Tick(ctx)
	}
	if next, _ := f.db.GetJob(ctx, next.ID); next.CUPSID != 0 || next.State != "queued" {
		t.Fatal("later task was submitted while the canceled backend remained alive")
	}
	if device.calls != 0 {
		t.Fatal("device was reset before the synthetic backend exited")
	}
	timedCancel := os.Getenv("PRINTBRIDGE_TEST_CANCEL_DELAY") == "5"
	if !timedCancel {
		marker("recover")
		marker("finish")
	}
	waitSyntheticWithin(t, 20*time.Second, func() bool {
		list, err := client.List(ctx)
		return err == nil && len(list) == 1 && list[0].State == "idle"
	})
	// The next synthetic job can finish normally; the canceled backend above
	// was terminated by CUPS without these cooperative markers.
	marker("recover")
	marker("finish")
	f.service.Tick(ctx)
	if got := f.job(t, first.ID); got.State != "canceling" || got.Problem != "device_cancel_failed" {
		t.Fatal("idle was treated as proof of cleared printer memory", got.State, got.Problem)
	}
	if _, err := f.service.ConfirmCancel(ctx, "owner", first.ID, true); !errors.Is(err, printing.ErrConflict) {
		t.Fatal("client assertion bypassed failed hardware verification", err)
	}
	device.err = nil
	f.service.Tick(ctx)
	f.service.Tick(ctx)
	next, err = f.db.GetJob(ctx, next.ID)
	if err != nil || next.CUPSID == 0 || next.State != "printing" {
		t.Fatal("confirmed cancellation did not allow the next synthetic task", next.State, next.Problem, err)
	}
	waitSynthetic(t, func() bool {
		f.service.Tick(ctx)
		next, err := f.db.GetJob(ctx, next.ID)
		return err == nil && next.State == "completed"
	})
	if data, err := os.ReadFile(filepath.Join(root, "received")); err != nil || string(data) != "synthetic-next" {
		t.Fatal("later synthetic task did not deliver its own data", err)
	}
}

func waitSynthetic(t *testing.T, ready func() bool) {
	t.Helper()
	waitSyntheticWithin(t, 8*time.Second, ready)
}

func waitSyntheticWithin(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("isolated synthetic CUPS condition timed out")
}
