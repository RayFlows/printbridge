package printing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/printers"
	"github.com/RayFlows/printbridge/server/internal/printing"
	"github.com/RayFlows/printbridge/server/internal/store"
)

type source struct{}

func (source) Discard(context.Context, string, string) error { return nil }

func (source) CopySource(_ context.Context, owner, id string, out io.Writer) (documents.Document, error) {
	if owner != "owner" || id != "doc" {
		return documents.Document{}, documents.ErrNotFound
	}
	_, err := io.WriteString(out, "synthetic")
	return documents.Document{Name: "synthetic.pdf", PageCount: 17}, err
}

type processor struct {
	fail           bool
	phasePages     [][]int
	phaseRotations []int
	renderPages    []int
}

func (p *processor) Layout(_ context.Context, _, output string, s printing.Settings, pages []int) (int, error) {
	if p.fail {
		return 0, documents.ErrConversion
	}
	n := (len(pages) + s.PagesPerSheet - 1) / s.PagesPerSheet
	if s.Duplex() && n%2 != 0 {
		n++
	}
	return n, os.WriteFile(output, []byte("synthetic"), 0600)
}
func (p *processor) Phase(_ context.Context, _, output string, pages []int, rotation int) error {
	p.phasePages = append(p.phasePages, append([]int(nil), pages...))
	p.phaseRotations = append(p.phaseRotations, rotation)
	return os.WriteFile(output, []byte("synthetic"), 0600)
}

func TestPrepareReplacesOnlyUnsubmittedOwnedPlans(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	original := f.plan(t, false)
	id := original.ID
	for i := 0; i < 70; i++ {
		s := settings()
		s.Copies = 2 + i%2
		p, err := f.service.Prepare(ctx, "owner", "doc", s, id)
		if err != nil {
			t.Fatal(i, err)
		}
		id = p.ID
	}
	plans, _ := f.db.ListPlans(ctx)
	entries, _ := os.ReadDir(filepath.Join(f.root, "plans"))
	if len(plans) != 1 || len(entries) != 1 || f.backend.creates != 0 {
		t.Fatal("automatic previews accumulated or printed")
	}
	if _, err := f.service.Plan(ctx, "owner", original.ID); !errors.Is(err, printing.ErrNotFound) {
		t.Fatal("superseded plan remained accessible", err)
	}
	current := plans[0]
	reused, err := f.service.Prepare(ctx, "owner", "doc", current.Settings, current.ID)
	if err != nil || reused.ID != current.ID {
		t.Fatal("identical preview not reused", reused, err)
	}
	f.processor.fail = true
	if _, err := f.service.Prepare(ctx, "owner", "doc", settings(), current.ID); !errors.Is(err, documents.ErrConversion) {
		t.Fatal(err)
	}
	f.processor.fail = false
	if _, err := f.service.Plan(ctx, "owner", current.ID); err != nil {
		t.Fatal("failed replacement deleted prior plan", err)
	}
	for _, tt := range []struct{ owner, doc, id string }{
		{"other", "doc", current.ID}, {"owner", "another-doc", current.ID},
	} {
		if _, err := f.service.Prepare(ctx, tt.owner, tt.doc, settings(), tt.id); !errors.Is(err, printing.ErrNotFound) {
			t.Fatal("cross-owner/document replacement", err)
		}
	}
	if _, err := f.service.Prepare(ctx, "owner", "doc", settings(), "../bad"); !errors.Is(err, printing.ErrSettings) {
		t.Fatal("invalid predecessor accepted", err)
	}
	job := f.create(t, current, "request-0000000001")
	if _, err := f.service.Prepare(ctx, "owner", "doc", settings(), current.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Plan(ctx, "owner", current.ID); err != nil || f.job(t, job.ID).PlanID != current.ID {
		t.Fatal("submitted snapshot changed", err)
	}
	// A retry may still name a predecessor already replaced by a lost response.
	if _, err := f.service.Prepare(ctx, "owner", "doc", settings(), original.ID); err != nil {
		t.Fatal("lost-response retry rejected", err)
	}
}

func TestImageInversionRejectsDocumentWithoutReplacingPreviousPlan(t *testing.T) {
	f := setup(t)
	previous := f.plan(t, false)
	s := previous.Settings
	s.InvertImages = true
	if _, err := f.service.Prepare(context.Background(), "owner", "doc", s, previous.ID); !errors.Is(err, printing.ErrSettings) {
		t.Fatal("non-image inversion accepted", err)
	}
	if _, err := f.service.Plan(context.Background(), "owner", previous.ID); err != nil {
		t.Fatal("previous preview lost on failure", err)
	}
}

func TestPrepareReordersLayoutAndSheetMapTogether(t *testing.T) {
	f := setup(t)
	s := settings()
	s.Pages, s.Sides, s.Calibrated, s.OutputOrder = "1-3", "manual-long-edge", true, "reverse"
	p, err := f.service.Prepare(context.Background(), "owner", "doc", s, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}}
	if !reflect.DeepEqual(p.Sheets, want) || !reflect.DeepEqual(f.processor.phasePages, [][]int{{3, 4, 1, 2}}) {
		t.Fatal("plan and prepared PDF differ", p.Sheets, f.processor.phasePages)
	}
	if !reflect.DeepEqual(p.SelectedPages, []int{1, 2, 3}) || f.backend.creates != 0 {
		t.Fatal("source pages reversed or preview printed")
	}
}

func TestReadingPreviewMapsIntoPrintedLayoutAndHidesOnlyPadding(t *testing.T) {
	for _, tc := range []struct {
		name, sides, order, pages string
		nup                       int
		want                      []int
	}{
		{"simplex reverse", "one-sided", "reverse", "1-3", 1, []int{3, 2, 1}},
		{"simplex forward", "one-sided", "normal", "1-3", 1, []int{1, 2, 3}},
		{"manual reverse", "manual-long-edge", "reverse", "1-3", 1, []int{3, 4, 1}},
		{"automatic reverse", "two-sided-long-edge", "reverse", "1-3", 1, []int{3, 4, 1}},
		{"manual forward", "manual-long-edge", "normal", "1-3", 1, []int{1, 2, 3}},
		{"nup ranges", "manual-long-edge", "reverse", "2-4,9,17", 2, []int{3, 4, 1}},
		{"one face", "manual-long-edge", "reverse", "1", 2, []int{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			s := settings()
			s.Pages, s.Sides, s.OutputOrder, s.PagesPerSheet = tc.pages, tc.sides, tc.order, tc.nup
			s.Calibrated = true
			p, err := f.service.Prepare(ctx, "owner", "doc", s, "")
			if err != nil {
				t.Fatal(err)
			}
			if p.PreviewPageCount != len(tc.want) {
				t.Fatal("incorrect reading count", p)
			}
			// Persist the old payload shape to exercise pre-upgrade plans.
			p.PreviewPageCount = 0
			if err := f.db.DeletePlan(ctx, p.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.db.SavePlan(ctx, p, ""); err != nil {
				t.Fatal(err)
			}
			restored, err := f.service.Plan(ctx, "owner", p.ID)
			if err != nil || restored.PreviewPageCount != len(tc.want) {
				t.Fatal("old plan lost reading count", restored, err)
			}
			for i := range tc.want {
				if _, err := f.service.Preview(ctx, "owner", p.ID, i+1, "reading"); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(f.processor.renderPages, tc.want) {
				t.Fatal("preview not in reading order", f.processor.renderPages, tc.want)
			}
			// Physical and reading indexes share pixels only when they map to the same face.
			before := len(f.processor.renderPages)
			if _, err := f.service.Preview(ctx, "owner", p.ID, tc.want[0], "print"); err != nil {
				t.Fatal(err)
			}
			if len(f.processor.renderPages) != before {
				t.Fatal("equivalent physical face missed the cache")
			}
			for _, n := range []int{0, len(tc.want) + 1} {
				if _, err := f.service.Preview(ctx, "owner", p.ID, n, "reading"); !errors.Is(err, documents.ErrPage) {
					t.Fatal("reading boundary accepted", n, err)
				}
			}
			if _, err := f.service.Preview(ctx, "owner", p.ID, 1, "invalid"); !errors.Is(err, printing.ErrSettings) {
				t.Fatal(err)
			}
			if _, err := f.service.Preview(ctx, "other", p.ID, 1, "reading"); !errors.Is(err, printing.ErrNotFound) {
				t.Fatal("cache bypassed ownership", err)
			}
			if _, err := f.service.Preview(ctx, "owner", p.ID, p.PageCount, ""); err != nil {
				t.Fatal("legacy padded preview unavailable", err)
			}
			if f.backend.creates != 0 {
				t.Fatal("preview submitted printing")
			}
		})
	}
}

func TestEvenFirstManualWaitAndRestart(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	s := settings()
	s.Pages, s.Sides, s.Calibrated, s.OutputOrder = "1-3", "manual-long-edge", true, "reverse"
	if err := json.Unmarshal([]byte(`{"manualFirstSide":"back"}`), &s); err != nil {
		t.Fatal(err)
	}
	plan, err := f.service.Prepare(ctx, "owner", "doc", s, "")
	if err != nil {
		t.Fatal(err)
	}
	f.processor.phasePages = nil
	job := f.create(t, plan, "request-0000000001")
	if job.Phase != "back" {
		t.Fatalf("first pass = %s; want even faces (blank, 2)", job.Phase)
	}
	// The reversed layout is [3, blank, 1, 2], so select faces [2,4] then [1,3].
	if !reflect.DeepEqual(f.processor.phasePages, [][]int{{2, 4}, {1, 3}}) {
		t.Fatal("incorrect phase files", f.processor.phasePages)
	}
	if _, err := f.service.Continue(ctx, "owner", job.ID, true); !errors.Is(err, printing.ErrConflict) {
		t.Fatal("first pass was mistaken for an already continued task", err)
	}
	f.service.Tick(ctx)
	f.backend.complete(1)
	f.service.Tick(ctx)
	if f.job(t, job.ID).State != "awaiting_flip" {
		t.Fatal("even pass completed the whole job instead of waiting for paper")
	}
	restarted, err := printing.New(f.root, f.db, source{}, f.processor, f.backend)
	if err != nil {
		t.Fatal(err)
	}
	f.service = restarted
	f.service.Tick(ctx)
	if f.backend.creates != 1 {
		t.Fatal("restart printed without flip confirmation")
	}
	if _, err := f.service.Continue(ctx, "owner", job.ID, false); !errors.Is(err, printing.ErrConflict) {
		t.Fatal("paper confirmation was bypassed", err)
	}
	for i := 0; i < 2; i++ {
		continued, err := f.service.Continue(ctx, "owner", job.ID, true)
		if err != nil || continued.Phase != "front" {
			t.Fatal("second pass must be the odd faces", continued, err)
		}
	}
	f.service.Tick(ctx)
	f.backend.complete(2)
	f.service.Tick(ctx)
	if f.job(t, job.ID).State != "completed" || f.backend.creates != 2 || f.backend.sends != 2 {
		t.Fatal("second pass was duplicated or did not complete", f.job(t, job.ID))
	}
}

func TestEvenFirstRotatesAndReversesOnlySecondPass(t *testing.T) {
	for _, edge := range []string{"manual-long-edge", "manual-short-edge"} {
		for _, rotation := range []int{0, 180} {
			f := setup(t)
			s := settings()
			s.Pages, s.Sides, s.Calibrated, s.OutputOrder = "1-3", edge, true, "reverse"
			s.ManualFirstSide, s.BackOrder, s.BackRotation = "back", "reverse", rotation
			plan, err := f.service.Prepare(context.Background(), "owner", "doc", s, "")
			if err != nil {
				t.Fatal(err)
			}
			f.processor.phasePages, f.processor.phaseRotations = nil, nil
			f.create(t, plan, "request-0000000001")
			wantRotation := rotation
			if edge == "manual-short-edge" {
				wantRotation = (wantRotation + 180) % 360
			}
			if !reflect.DeepEqual(f.processor.phasePages, [][]int{{2, 4}, {3, 1}}) ||
				!reflect.DeepEqual(f.processor.phaseRotations, []int{0, wantRotation}) {
				t.Fatal("calibration applied to the wrong pass", f.processor.phasePages, f.processor.phaseRotations)
			}
		}
	}
}

func TestEvenFirstCancellationNeverStartsAnotherPass(t *testing.T) {
	for _, point := range []string{"queued", "printing", "awaiting_flip", "second queued", "second printing"} {
		t.Run(point, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			s := settings()
			s.Sides, s.ManualFirstSide, s.Calibrated = "manual-long-edge", "back", true
			plan, err := f.service.Prepare(ctx, "owner", "doc", s, "")
			if err != nil {
				t.Fatal(err)
			}
			job := f.create(t, plan, "request-0000000001")
			if point != "queued" {
				f.service.Tick(ctx)
			}
			if point == "awaiting_flip" || point == "second queued" || point == "second printing" {
				f.backend.complete(1)
				f.service.Tick(ctx)
			}
			if point == "second queued" || point == "second printing" {
				if _, err := f.service.Continue(ctx, "owner", job.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			if point == "second printing" {
				f.service.Tick(ctx)
			}
			creates := f.backend.creates
			if _, err := f.service.Cancel(ctx, "owner", job.ID); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				f.service.Tick(ctx)
			}
			if f.job(t, job.ID).DeviceCancelRequired {
				if _, err := f.service.ConfirmCancel(ctx, "owner", job.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			if f.job(t, job.ID).State != "canceled" || f.backend.creates != creates {
				t.Fatal("cancellation created an extra pass", f.job(t, job.ID))
			}
			if _, err := f.service.Continue(ctx, "owner", job.ID, true); !errors.Is(err, printing.ErrConflict) {
				t.Fatal("canceled job resumed", err)
			}
		})
	}
}

func TestEvenFirstSecondPassUncertainSubmissionSurvivesRestart(t *testing.T) {
	for _, scenario := range []string{"create", "send", "release"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			s := settings()
			s.Sides, s.ManualFirstSide, s.Calibrated = "manual-long-edge", "back", true
			plan, err := f.service.Prepare(ctx, "owner", "doc", s, "")
			if err != nil {
				t.Fatal(err)
			}
			job := f.create(t, plan, "request-0000000001")
			f.service.Tick(ctx)
			f.backend.complete(1)
			f.service.Tick(ctx)
			if _, err := f.service.Continue(ctx, "owner", job.ID, true); err != nil {
				t.Fatal(err)
			}
			f.backend.createError = scenario == "create"
			f.backend.sendError, f.backend.acceptSend = scenario == "send", true
			f.backend.releaseError = scenario == "release"
			f.service.Tick(ctx)
			f.service, err = printing.New(f.root, f.db, source{}, f.processor, f.backend)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				f.service.Tick(ctx)
			}
			if f.backend.creates != 2 || f.backend.sends != 2 || f.backend.releases != 2 ||
				f.job(t, job.ID).Phase != "front" {
				t.Fatal("restart duplicated the second pass", f.job(t, job.ID))
			}
			f.backend.complete(2)
			f.service.Tick(ctx)
			if f.job(t, job.ID).State != "completed" {
				t.Fatal("odd second pass did not complete", f.job(t, job.ID))
			}
		})
	}
}
func (p *processor) RenderPreview(ctx context.Context, source string, page int, output, _ string) error {
	return p.Render(ctx, source, page, output)
}

func (p *processor) Render(_ context.Context, _ string, page int, output string) error {
	p.renderPages = append(p.renderPages, page)
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	return errors.Join(png.Encode(f, image.NewRGBA(image.Rect(0, 0, 8, 12))), f.Close())
}

type backend struct {
	jobs                                 map[int]printing.Status
	creates, sends, releases             int
	createError, sendError, releaseError bool
	acceptSend, unsafeHold               bool
	rejected                             bool
	printerState                         string
}

func (b *backend) List(context.Context) ([]printers.Printer, error) {
	if b.printerState == "missing" {
		return []printers.Printer{}, nil
	}
	if b.printerState == "unavailable" {
		return nil, printers.ErrUnavailable
	}
	p := printer()
	p.State = "idle"
	if b.printerState != "" {
		p.State = b.printerState
	}
	if b.printerState == "disconnected" {
		p.State, p.DeviceState = "idle", "disconnected"
	}
	return []printers.Printer{p}, nil
}
func (b *backend) CreateHeld(_ context.Context, j printing.Job) (int, error) {
	b.creates++
	id := b.creates
	hold := "indefinite"
	if b.unsafeHold {
		hold = "no-hold"
	}
	b.jobs[id] = printing.Status{ID: id, Name: printing.JobName(j), State: 4, Documents: 0, Hold: hold}
	if b.rejected {
		return id, printing.ErrSettings
	}
	if b.createError {
		return 0, printing.ErrBackend
	}
	return id, nil
}
func (b *backend) Send(_ context.Context, id int, _ string) error {
	b.sends++
	if !b.sendError || b.acceptSend {
		j := b.jobs[id]
		j.Documents = 1
		b.jobs[id] = j
	}
	if b.sendError {
		return printing.ErrBackend
	}
	return nil
}
func (b *backend) Release(_ context.Context, id int) error {
	b.releases++
	j := b.jobs[id]
	j.State = 5
	b.jobs[id] = j
	if b.releaseError {
		return printing.ErrBackend
	}
	return nil
}
func (b *backend) Status(_ context.Context, id int) (printing.Status, error) {
	j, ok := b.jobs[id]
	if !ok {
		return j, printing.ErrNotFound
	}
	return j, nil
}
func (b *backend) Find(_ context.Context, _, name string) ([]printing.Status, error) {
	result := []printing.Status{}
	for _, j := range b.jobs {
		if j.Name == name {
			result = append(result, j)
		}
	}
	return result, nil
}
func (b *backend) Cancel(_ context.Context, id int) error {
	j := b.jobs[id]
	j.State = 7
	b.jobs[id] = j
	return nil
}
func (b *backend) complete(id int) { j := b.jobs[id]; j.State = 9; b.jobs[id] = j }

type fixture struct {
	service   *printing.Service
	db        *store.SQLite
	backend   *backend
	processor *processor
	root      string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	b := &backend{jobs: map[int]printing.Status{}}
	p := &processor{}
	root := filepath.Join(dir, "print")
	s, err := printing.New(root, db, source{}, p, b)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{s, db, b, p, root}
}
func (f *fixture) plan(t *testing.T, manual bool) printing.Plan {
	t.Helper()
	s := settings()
	if manual {
		s.Sides = "manual-long-edge"
		s.Calibrated = true
	}
	p, err := f.service.Prepare(context.Background(), "owner", "doc", s, "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *fixture) create(t *testing.T, p printing.Plan, key string) printing.Job {
	t.Helper()
	j, err := f.service.Create(context.Background(), "owner", p.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func (f *fixture) job(t *testing.T, id string) printing.Job {
	t.Helper()
	j, err := f.service.Job(context.Background(), "owner", id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func TestPrepareOwnershipAndIdempotentCreation(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	p := f.plan(t, false)
	if _, err := f.service.Preview(ctx, "owner", p.ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	if f.backend.creates != 0 {
		t.Fatal("preview printed")
	}
	if _, err := f.service.Plan(ctx, "other", p.ID); !errors.Is(err, printing.ErrNotFound) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan printing.Job, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			j, err := f.service.Create(ctx, "owner", p.ID, "request-0000000001")
			if err != nil {
				t.Error(err)
				return
			}
			results <- j
		})
	}
	wg.Wait()
	close(results)
	first := ""
	for j := range results {
		if first == "" {
			first = j.ID
		}
		if first != j.ID {
			t.Fatal("duplicate job")
		}
	}
	if f.backend.creates != 0 {
		t.Fatal("HTTP create bypassed scheduler")
	}
	another := f.plan(t, false)
	if _, err := f.service.Create(ctx, "owner", another.ID, "request-0000000001"); !errors.Is(err, printing.ErrConflict) {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	if f.backend.creates != 1 || f.backend.sends != 1 || f.backend.releases != 1 {
		t.Fatal(f.backend)
	}
	f.backend.complete(1)
	f.service.Tick(ctx)
	if f.job(t, first).State != "completed" {
		t.Fatal("completion lost")
	}
}
func TestManualWaitBlocksOtherJobsAndSurvivesRestart(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	j := f.create(t, f.plan(t, true), "request-0000000001")
	// Older deployments can already contain a backlog. Keep recovering it.
	nextPlan := f.plan(t, false)
	next := printing.Job{ID: "ffffffffffffffffffffffffffffffff", PlanID: nextPlan.ID, Owner: "owner",
		Key: "request-0000000002", Name: nextPlan.Name, Settings: nextPlan.Settings, SheetCount: nextPlan.SheetCount,
		State: "queued", Phase: "single", CUPSJobIDs: []int{}, CreatedAt: j.CreatedAt.Add(time.Second), UpdatedAt: time.Now()}
	if err := f.db.InsertJob(ctx, next); err != nil {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	f.backend.complete(1)
	f.service.Tick(ctx)
	if f.job(t, j.ID).State != "awaiting_flip" {
		t.Fatal("did not wait")
	}
	restarted, err := printing.New(f.root, f.db, source{}, f.processor, f.backend)
	if err != nil {
		t.Fatal(err)
	}
	f.service = restarted
	for i := 0; i < 3; i++ {
		f.service.Tick(ctx)
	}
	if f.backend.creates != 1 {
		t.Fatal("interleaved while waiting for paper")
	}
	if _, err := f.service.Continue(ctx, "owner", j.ID, false); !errors.Is(err, printing.ErrConflict) {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.service.Continue(ctx, "owner", j.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	f.service.Tick(ctx)
	if f.backend.creates != 2 || f.job(t, j.ID).Phase != "back" {
		t.Fatal("back was duplicated or skipped")
	}
	f.backend.complete(2)
	f.service.Tick(ctx)
	f.service.Tick(ctx)
	if f.job(t, j.ID).State != "completed" || f.backend.creates != 3 {
		t.Fatal("printer lock not released")
	}
}
func TestUncertainSubmissionIsReconciledWithoutDuplicateSend(t *testing.T) {
	for _, scenario := range []string{"create", "send accepted", "send unknown", "release", "unsafe hold"} {
		t.Run(scenario, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			switch scenario {
			case "create":
				f.backend.createError = true
			case "send accepted":
				f.backend.sendError = true
				f.backend.acceptSend = true
			case "send unknown":
				f.backend.sendError = true
			case "release":
				f.backend.releaseError = true
			case "unsafe hold":
				f.backend.unsafeHold = true
			}
			j := f.create(t, f.plan(t, false), "request-0000000001")
			f.service.Tick(ctx)
			restarted, err := printing.New(f.root, f.db, source{}, f.processor, f.backend)
			if err != nil {
				t.Fatal(err)
			}
			f.service = restarted
			for i := 0; i < 3; i++ {
				f.service.Tick(ctx)
			}
			if f.backend.creates != 1 || f.backend.sends > 1 || f.backend.releases > 1 {
				t.Fatal("duplicate side effect", f.backend)
			}
			if scenario == "send unknown" || scenario == "unsafe hold" {
				if f.backend.releases != 0 || f.job(t, j.ID).Problem != "submission_uncertain" {
					t.Fatal("uncertain job was printed")
				}
			} else if f.job(t, j.ID).State != "printing" {
				t.Fatal(f.job(t, j.ID))
			}
		})
	}
}
func TestRejectedAttributesCancelEmptyJobWithoutSending(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.backend.rejected = true
	job := f.create(t, f.plan(t, false), "request-0000000001")
	for i := 0; i < 3; i++ {
		f.service.Tick(ctx)
	}
	got := f.job(t, job.ID)
	if got.State != "failed" || got.Problem != "settings_rejected" || f.backend.sends != 0 || f.backend.releases != 0 {
		t.Fatal("substituted settings were treated as a recoverable send", got)
	}
}

func TestCancelAndCleanup(t *testing.T) {
	t.Parallel()
	f := setup(t)
	ctx := context.Background()
	j := f.create(t, f.plan(t, true), "request-0000000001")
	if _, err := f.service.Cancel(ctx, "other", j.ID); !errors.Is(err, printing.ErrNotFound) {
		t.Fatal(err)
	}
	canceled, err := f.service.Cancel(ctx, "owner", j.ID)
	if err != nil || canceled.State != "canceled" {
		t.Fatal(canceled, err)
	}
	f.service.Tick(ctx)
	if f.backend.creates != 0 {
		t.Fatal("canceled queue printed")
	}
	j = f.create(t, f.plan(t, true), "request-0000000002")
	f.service.Tick(ctx)
	if _, err := f.service.Cancel(ctx, "owner", j.ID); err != nil {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	f.service.Tick(ctx)
	if _, err := f.service.ConfirmCancel(ctx, "owner", j.ID, true); err != nil {
		t.Fatal(err)
	}
	if f.job(t, j.ID).State != "canceled" {
		t.Fatal("cancel not confirmed")
	}
	f.processor.fail = true
	if _, err := f.service.Prepare(ctx, "owner", "doc", settings(), ""); !errors.Is(err, documents.ErrConversion) {
		t.Fatal(err)
	}
	plans, _ := f.db.ListPlans(ctx)
	entries, _ := os.ReadDir(filepath.Join(f.root, "plans"))
	if len(plans) != len(entries) {
		t.Fatal("failed preparation left files")
	}
	canceled = f.job(t, j.ID)
	canceled.UpdatedAt = time.Now().Add(-8 * 24 * time.Hour)
	f.db.SaveJob(ctx, canceled)
	if err := f.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Job(ctx, "owner", j.ID); !errors.Is(err, printing.ErrNotFound) {
		t.Fatal("history unbounded", err)
	}
}

func TestCanceledJobWaitsForBackendExitBeforeAllowingNextJob(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first := f.create(t, f.plan(t, false), "request-0000000001")
	f.service.Tick(ctx)
	f.backend.printerState = "processing"
	if _, err := f.service.Cancel(ctx, "owner", first.ID); err != nil {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	f.service.Tick(ctx)
	if got := f.job(t, first.ID); got.State != "canceling" {
		t.Fatalf("CUPS canceled the job but its backend still holds old print data: state=%s", got.State)
	}
	nextPlan := f.plan(t, false)
	if _, err := f.service.Create(ctx, "owner", nextPlan.ID, "request-0000000003"); !errors.Is(err, printing.ErrActiveJob) {
		t.Fatal("same owner bypassed the pending cancellation", err)
	}
	next := printing.Job{ID: "ffffffffffffffffffffffffffffffff", PlanID: nextPlan.ID, Owner: "other",
		Key: "request-0000000002", Name: nextPlan.Name, Settings: nextPlan.Settings, SheetCount: nextPlan.SheetCount,
		State: "queued", Phase: "single", CreatedAt: first.CreatedAt.Add(-time.Second), UpdatedAt: time.Now()}
	if err := f.db.InsertJob(ctx, next); err != nil {
		t.Fatal(err)
	}
	restarted, err := printing.New(f.root, f.db, source{}, f.processor, f.backend)
	if err != nil {
		t.Fatal(err)
	}
	f.service = restarted
	for i := 0; i < 3; i++ {
		f.service.Tick(ctx)
	}
	if f.backend.creates != 1 || f.backend.sends != 1 || f.backend.releases != 1 {
		t.Fatal("new job reached the printer before the old backend exited", f.backend)
	}
	if err := f.service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "jobs", first.ID, "single.pdf")); err != nil {
		t.Fatal("uncertain cancellation removed the retained task", err)
	}
	f.backend.printerState = "idle"
	f.service.Tick(ctx)
	f.service.Tick(ctx)
	if f.backend.creates != 1 || f.job(t, first.ID).Problem != "device_cancel_required" {
		t.Fatal("queue idle was mistaken for cleared device buffers", f.job(t, first.ID))
	}
	if _, err := f.service.ConfirmCancel(ctx, "owner", first.ID, true); err != nil {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	if f.job(t, first.ID).State != "canceled" || f.backend.creates != 2 {
		t.Fatal("confirmed backend exit did not release the queue", f.job(t, first.ID), f.backend)
	}
}

func TestCancelConfirmationRequiresOwnerStoppedDeviceAndBackendExit(t *testing.T) {
	for _, state := range []string{"processing", "stopped", "unknown", "missing", "unavailable", "disconnected", "idle"} {
		t.Run(state, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			job := f.create(t, f.plan(t, false), "request-0000000001")
			if _, err := f.service.ConfirmCancel(ctx, "owner", job.ID, true); !errors.Is(err, printing.ErrConflict) {
				t.Fatal("unsubmitted job accepted device confirmation", err)
			}
			f.service.Tick(ctx)
			if _, err := f.service.Cancel(ctx, "owner", job.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.ConfirmCancel(ctx, "owner", job.ID, true); !errors.Is(err, printing.ErrConflict) {
				t.Fatal("live CUPS job accepted confirmation", err)
			}
			f.service.Tick(ctx)
			f.backend.printerState = state
			f.service.Tick(ctx)
			if _, err := f.service.ConfirmCancel(ctx, "other", job.ID, true); !errors.Is(err, printing.ErrNotFound) {
				t.Fatal("other owner cleared cancellation", err)
			}
			if _, err := f.service.ConfirmCancel(ctx, "owner", job.ID, false); !errors.Is(err, printing.ErrConflict) {
				t.Fatal("missing device confirmation accepted", err)
			}
			got, err := f.service.ConfirmCancel(ctx, "owner", job.ID, true)
			if state == "idle" {
				if err != nil || got.State != "canceled" {
					t.Fatal(got, err)
				}
				if _, err := f.service.ConfirmCancel(ctx, "owner", job.ID, true); err != nil {
					t.Fatal("confirmation was not idempotent", err)
				}
			} else if !errors.Is(err, printing.ErrConflict) || f.job(t, job.ID).State != "canceling" {
				t.Fatal("unready printer released cancellation barrier", got, err)
			}
		})
	}
}

func TestQueuedJobsWaitForExistingBackendWithoutSubmitting(t *testing.T) {
	for _, state := range []string{"processing", "stopped", "unknown", "missing", "unavailable", "disconnected"} {
		t.Run(state, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			job := f.create(t, f.plan(t, false), "request-0000000001")
			f.backend.printerState = state
			for i := 0; i < 3; i++ {
				f.service.Tick(ctx)
			}
			if got := f.job(t, job.ID); got.State != "queued" || got.Problem == "" || f.backend.creates != 0 {
				t.Fatal("unknown/busy printer accepted new data", got, f.backend)
			}
			f.backend.printerState = "idle"
			f.service.Tick(ctx)
			if f.job(t, job.ID).State != "printing" || f.backend.creates != 1 {
				t.Fatal("idle printer did not resume queued task")
			}
		})
	}
}

func TestExternalCancellationOrAbortRequiresClearingDevice(t *testing.T) {
	for _, state := range []int{7, 8, 9} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			job := f.create(t, f.plan(t, true), "request-0000000001")
			f.service.Tick(ctx)
			if state == 9 {
				if _, err := f.service.Cancel(ctx, "owner", job.ID); err != nil {
					t.Fatal(err)
				}
			}
			status := f.backend.jobs[1]
			status.State = state
			f.backend.jobs[1] = status
			f.service.Tick(ctx)
			current := f.job(t, job.ID)
			if current.State != "canceling" || !current.DeviceCancelRequired ||
				current.Problem != "device_cancel_required" {
				t.Fatal("terminal scheduler state bypassed device confirmation", current)
			}
			if _, err := f.service.Continue(ctx, "owner", job.ID, true); !errors.Is(err, printing.ErrConflict) {
				t.Fatal("canceling task started another print pass", err)
			}
			got, err := f.service.ConfirmCancel(ctx, "owner", job.ID, true)
			want := "canceled"
			if state == 8 {
				want = "failed"
			}
			if err != nil || got.State != want || got.DeviceCancelRequired || f.backend.creates != 1 {
				t.Fatal(got, err)
			}
		})
	}
}

type deviceCanceler struct {
	calls   int
	err     error
	printer string
}

func (d *deviceCanceler) CancelDevice(_ context.Context, printer string) error {
	d.calls++
	d.printer = printer
	return d.err
}

func TestAutomaticDeviceCancelWaitsForBackendAndSurvivesFailureAndRestart(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	device := &deviceCanceler{err: errors.New("device still reports MediaEmpty")}
	f.service.WithDeviceCanceler(device)
	job := f.create(t, f.plan(t, true), "request-0000000001")
	f.service.Tick(ctx)
	f.backend.printerState = "processing"
	if _, err := f.service.Cancel(ctx, "owner", job.ID); err != nil {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	f.service.Tick(ctx)
	if device.calls != 0 {
		t.Fatal("reset while USB backend was active")
	}
	f.backend.printerState = "idle"
	f.service.Tick(ctx)
	if device.calls != 1 || f.job(t, job.ID).State != "canceling" {
		t.Fatal("device failure bypassed barrier or device cancellation never attempted", device.calls, f.job(t, job.ID))
	}
	if _, err := f.service.ConfirmCancel(ctx, "owner", job.ID, true); !errors.Is(err, printing.ErrConflict) {
		t.Fatal("unverified client confirmation bypassed hardware check", err)
	}
	if _, err := f.service.Create(ctx, "owner", job.PlanID, "request-0000000002"); !errors.Is(err, printing.ErrActiveJob) {
		t.Fatal("device error released printer", err)
	}
	restarted, err := printing.New(f.root, f.db, source{}, f.processor, f.backend)
	if err != nil {
		t.Fatal(err)
	}
	f.service = restarted.WithDeviceCanceler(device)
	device.err = nil
	f.service.Tick(ctx)
	got := f.job(t, job.ID)
	if got.State != "canceled" || got.DeviceCancelRequired || device.calls != 2 ||
		device.printer != job.Settings.PrinterID || f.backend.creates != 1 {
		t.Fatal("automatic cancellation did not finish or started another duplex pass", got, device)
	}
	f.service.Tick(ctx)
	if device.calls != 2 {
		t.Fatal("terminal task reset device again")
	}
	f.create(t, f.plan(t, false), "request-0000000003")
	f.service.Tick(ctx)
	if f.backend.creates != 2 {
		t.Fatal("new task blocked after verified cancellation")
	}
}

func TestAutomaticDeviceCancelHandlesExternalAbortAndCompletionRace(t *testing.T) {
	for _, state := range []int{7, 8, 9} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			device := &deviceCanceler{}
			f.service.WithDeviceCanceler(device)
			job := f.create(t, f.plan(t, false), "request-0000000001")
			f.service.Tick(ctx)
			if state == 9 {
				if _, err := f.service.Cancel(ctx, "owner", job.ID); err != nil {
					t.Fatal(err)
				}
			}
			status := f.backend.jobs[1]
			status.State = state
			f.backend.jobs[1] = status
			f.service.Tick(ctx)
			want := "canceled"
			if state == 8 {
				want = "failed"
			}
			if got := f.job(t, job.ID); got.State != want || got.DeviceCancelRequired || device.calls != 1 {
				t.Fatal("terminal CUPS state skipped hardware cleanup", got, device.calls)
			}
		})
	}
}
