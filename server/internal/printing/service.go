package printing

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/printers"
)

type Repository interface {
	SavePlan(context.Context, Plan, string) error
	GetPlan(context.Context, string) (Plan, error)
	ListPlans(context.Context) ([]Plan, error)
	DeletePlan(context.Context, string) error
	InsertJob(context.Context, Job) error
	SaveJob(context.Context, Job) error
	GetJob(context.Context, string) (Job, error)
	FindJob(context.Context, string, string) (Job, error)
	ListJobs(context.Context) ([]Job, error)
	DeleteJob(context.Context, string) error
}
type DocumentSource interface {
	CopySource(context.Context, string, string, io.Writer) (documents.Document, error)
	Discard(context.Context, string, string) error
}
type Processor interface {
	Layout(context.Context, string, string, Settings, []int) (int, error)
	Phase(context.Context, string, string, []int, int) error
	RenderPreview(context.Context, string, int, string, string) error
}
type Status struct {
	ID        int
	Name      string
	State     int
	Documents int
	Hold      string
}
type Backend interface {
	List(context.Context) ([]printers.Printer, error)
	CreateHeld(context.Context, Job) (int, error)
	Send(context.Context, int, string) error
	Release(context.Context, int) error
	Status(context.Context, int) (Status, error)
	Find(context.Context, string, string) ([]Status, error)
	Cancel(context.Context, int) error
}

type DeviceCanceler interface {
	CancelDevice(context.Context, string) error
}

var recordID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var requestKey = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

const taskRetention = 3 * 24 * time.Hour

type Service struct {
	root         string
	repo         Repository
	docs         DocumentSource
	pdf          Processor
	backend      Backend
	mu           sync.Mutex
	gate         chan struct{}
	admission    func(context.Context) error
	deviceCancel DeviceCanceler
}

func (s *Service) WithDeviceCanceler(device DeviceCanceler) *Service {
	s.deviceCancel = device
	return s
}

func (s *Service) WithAdmission(check func(context.Context) error) *Service {
	s.admission = check
	return s
}

func (s *Service) admit(ctx context.Context) error {
	if s.admission != nil {
		return s.admission(ctx)
	}
	return nil
}

func New(root string, repo Repository, docs DocumentSource, pdf Processor, backend Backend) (*Service, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{"plans", "jobs"} {
		if err := os.MkdirAll(filepath.Join(absolute, dir), 0700); err != nil {
			return nil, err
		}
	}
	return &Service{root: absolute, repo: repo, docs: docs, pdf: pdf, backend: backend, gate: make(chan struct{}, 1)}, nil
}

func newID() string { var id [16]byte; rand.Read(id[:]); return hex.EncodeToString(id[:]) }
func (s *Service) acquire() error {
	select {
	case s.gate <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}

func (s *Service) Prepare(ctx context.Context, owner, documentID string, settings Settings, replacesPlanID string) (Plan, error) {
	if err := s.admit(ctx); err != nil {
		return Plan{}, err
	}
	if err := s.acquire(); err != nil {
		return Plan{}, err
	}
	defer func() { <-s.gate }()
	replacement := ""
	if replacesPlanID != "" {
		if !recordID.MatchString(replacesPlanID) {
			return Plan{}, ErrSettings
		}
		previous, err := s.repo.GetPlan(ctx, replacesPlanID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Plan{}, err
		}
		if err == nil {
			if previous.Owner != owner || previous.DocumentID != documentID {
				return Plan{}, ErrNotFound
			}
			if previous.Settings == settings && time.Now().Before(previous.ExpiresAt) {
				previous.setPreviewPageCount()
				return previous, nil
			}
			jobs, err := s.repo.ListJobs(ctx)
			if err != nil {
				return Plan{}, err
			}
			if !slices.ContainsFunc(jobs, func(job Job) bool { return job.PlanID == previous.ID }) {
				replacement = previous.ID
			}
		}
	}
	if err := s.checkSpace(128 << 20); err != nil {
		return Plan{}, err
	}
	all, err := s.repo.ListPlans(ctx)
	if err != nil {
		return Plan{}, err
	}
	counted := len(all)
	if replacement != "" {
		counted--
	}
	if counted >= 64 {
		return Plan{}, ErrCapacity
	}
	list, err := s.backend.List(ctx)
	if err != nil {
		return Plan{}, ErrBackend
	}
	var printer *printers.Printer
	for i := range list {
		if list[i].ID == settings.PrinterID {
			printer = &list[i]
			break
		}
	}
	if printer == nil {
		return Plan{}, ErrSettings
	}
	settings, err = resolveManualProfile(settings, *printer)
	if err != nil {
		return Plan{}, err
	}
	id := newID()
	dir := filepath.Join(s.root, "plans", id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return Plan{}, err
	}
	saved := false
	defer func() {
		if !saved {
			_ = os.RemoveAll(dir)
		}
	}()
	input := filepath.Join(dir, "input.pdf")
	file, err := os.OpenFile(input, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Plan{}, err
	}
	doc, copyErr := s.docs.CopySource(ctx, owner, documentID, file)
	closeErr := file.Close()
	if copyErr != nil {
		return Plan{}, copyErr
	}
	if closeErr != nil {
		return Plan{}, closeErr
	}
	if settings.InvertImages && !documents.IsImageName(doc.Name) {
		return Plan{}, ErrSettings
	}
	selected, err := ValidateSettings(settings, *printer, doc.PageCount)
	if err != nil {
		return Plan{}, err
	}
	sheets, faces := ArrangeSheets(selected, settings)
	output := filepath.Join(dir, "layout.pdf")
	draft := output
	if settings.OutputOrder == "reverse" {
		draft = filepath.Join(dir, "unordered.pdf")
	}
	count, err := s.pdf.Layout(ctx, input, draft, settings, selected)
	if err != nil {
		return Plan{}, err
	}
	if count != len(faces) {
		return Plan{}, documents.ErrConversion
	}
	if draft != output {
		if err := s.pdf.Phase(ctx, draft, output, faces, 0); err != nil {
			return Plan{}, err
		}
		_ = os.Remove(draft)
	}
	_ = os.Remove(input)
	if info, err := os.Stat(filepath.Join(dir, "layout.pdf")); err != nil || info.Size() > 128<<20 {
		return Plan{}, ErrCapacity
	}
	if err := s.checkSpace(0); err != nil {
		return Plan{}, err
	}
	now := time.Now().UTC()
	plan := Plan{ID: id, DocumentID: documentID, Name: doc.Name, Owner: owner, Settings: settings, SelectedPages: selected,
		PageCount: count, SheetCount: len(sheets) * settings.Copies, Sheets: sheets, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	plan.setPreviewPageCount()
	if err := s.repo.SavePlan(ctx, plan, replacement); err != nil {
		return Plan{}, err
	}
	saved = true
	if replacement != "" {
		// Metadata is replaced atomically; cleanup also collects orphan directories
		// if a crash or a temporary filesystem error interrupts removal.
		_ = os.RemoveAll(filepath.Join(s.root, "plans", replacement))
	}
	return plan, nil
}

func (s *Service) Plan(ctx context.Context, owner, id string) (Plan, error) {
	if !recordID.MatchString(id) {
		return Plan{}, ErrNotFound
	}
	p, err := s.repo.GetPlan(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	if p.Owner != owner || !time.Now().Before(p.ExpiresAt) {
		return Plan{}, ErrNotFound
	}
	p.setPreviewPageCount()
	return p, nil
}

func (s *Service) Preview(ctx context.Context, owner, id string, page int, order string) ([]byte, error) {
	if err := s.acquire(); err != nil {
		return nil, err
	}
	defer func() { <-s.gate }()
	plan, err := s.Plan(ctx, owner, id)
	if err != nil {
		return nil, err
	}
	page, err = plan.previewPage(page, order)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.root, "plans", id)
	// Legacy page-N images ignored colorMode, including on monochrome plans.
	cached := filepath.Join(dir, fmt.Sprintf("page-color-v1-%d.png", page))
	if data, err := readPNG(cached); err == nil {
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.CreateTemp(dir, "preview-*.png")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	file.Close()
	defer os.Remove(path)
	if err := s.pdf.RenderPreview(ctx, filepath.Join(dir, "layout.pdf"), page, path, plan.Settings.ColorMode); err != nil {
		return nil, err
	}
	data, err := readPNG(path)
	if err != nil {
		return nil, err
	}
	if err := s.checkSpace(0); err != nil {
		return nil, err
	}
	if err := os.Rename(path, cached); err != nil {
		return nil, err
	}
	return data, nil
}

func readPNG(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, documents.MaxPreviewBytes+1))
	if err != nil || int64(len(data)) > documents.MaxPreviewBytes {
		return nil, documents.ErrPreview
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 1600 || config.Height > 1600 {
		return nil, documents.ErrPreview
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, documents.ErrPreview
	}
	return data, nil
}

func (s *Service) Create(ctx context.Context, owner, planID, key string) (Job, error) {
	if !requestKey.MatchString(key) {
		return Job{}, ErrSettings
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, err := s.repo.FindJob(ctx, owner, key); err == nil {
		if previous.PlanID != planID {
			return Job{}, ErrConflict
		}
		return previous, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Job{}, err
	}
	if err := s.admit(ctx); err != nil {
		return Job{}, err
	}
	if err := s.acquire(); err != nil {
		return Job{}, err
	}
	defer func() { <-s.gate }()
	if err := s.checkSpace(512 << 20); err != nil {
		return Job{}, err
	}
	plan, err := s.Plan(ctx, owner, planID)
	if err != nil {
		return Job{}, err
	}
	all, err := s.repo.ListJobs(ctx)
	if err != nil {
		return Job{}, err
	}
	active := 0
	for _, job := range all {
		if !job.Terminal() {
			if job.Owner == owner && job.Settings.PrinterID == plan.Settings.PrinterID {
				return Job{}, ErrActiveJob
			}
			active++
		}
	}
	if active >= 32 {
		return Job{}, ErrCapacity
	}
	id := newID()
	dir := filepath.Join(s.root, "jobs", id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return Job{}, err
	}
	saved := false
	defer func() {
		if !saved {
			_ = os.RemoveAll(dir)
		}
	}()
	phases := []string{plan.Settings.FirstPhase()}
	if plan.Settings.Manual() {
		phases = append(phases, plan.Settings.SecondPhase())
	}
	for _, phase := range phases {
		pages := PhasePages(plan.PageCount, plan.Settings, phase)
		rotation := 0
		if phase == plan.Settings.SecondPhase() {
			rotation = plan.Settings.BackRotation
			if plan.Settings.Sides == "manual-short-edge" {
				rotation = (rotation + 180) % 360
			}
		}
		output := filepath.Join(dir, phase+".pdf")
		if err := s.pdf.Phase(ctx, filepath.Join(s.root, "plans", plan.ID, "layout.pdf"), output, pages, rotation); err != nil {
			return Job{}, err
		}
		if info, err := os.Stat(output); err != nil || info.Size() > 256<<20 {
			return Job{}, ErrCapacity
		}
	}
	if err := s.checkSpace(0); err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	job := Job{ID: id, PlanID: plan.ID, Owner: owner, Key: key, Name: plan.Name, Settings: plan.Settings,
		SheetCount: plan.SheetCount, State: "queued", Phase: phases[0], CUPSJobIDs: []int{}, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.InsertJob(ctx, job); err != nil {
		return Job{}, err
	}
	saved = true
	return job, nil
}

func (s *Service) Job(ctx context.Context, owner, id string) (Job, error) {
	if !recordID.MatchString(id) {
		return Job{}, ErrNotFound
	}
	job, err := s.repo.GetJob(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if job.Owner != owner {
		return Job{}, ErrNotFound
	}
	return job, nil
}
func (s *Service) Jobs(ctx context.Context, owner string, activeOnly bool) ([]Job, error) {
	all, err := s.repo.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	result := []Job{}
	for _, job := range all {
		if job.Owner == owner && (!activeOnly || !job.Terminal()) {
			result = append(result, job)
		}
	}
	slices.SortFunc(result, func(a, b Job) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return result, nil
}
func (s *Service) Continue(ctx context.Context, owner, id string, confirmed bool) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.Job(ctx, owner, id)
	if err != nil {
		return Job{}, err
	}
	if !time.Now().Before(job.CreatedAt.Add(taskRetention)) {
		if err := s.expire(ctx, &job); err != nil {
			return Job{}, err
		}
		return Job{}, ErrConflict
	}
	secondPhase := job.Settings.SecondPhase()
	if secondPhase != "" && job.Phase == secondPhase && !job.Terminal() && confirmed {
		return job, nil
	}
	if secondPhase == "" || job.Phase != job.Settings.FirstPhase() || job.State != "awaiting_flip" || !confirmed {
		return Job{}, ErrConflict
	}
	job.Phase = secondPhase
	job.CUPSID = 0
	job.CUPSState = 0
	job.State = "queued"
	job.Problem = ""
	err = s.save(ctx, &job)
	return job, err
}
func (s *Service) Cancel(ctx context.Context, owner, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.Job(ctx, owner, id)
	if err != nil {
		return Job{}, err
	}
	if job.Terminal() {
		return job, nil
	}
	if job.State == "queued" || job.State == "awaiting_flip" {
		job.State = "canceled"
	} else {
		job.DeviceCancelRequired = job.DeviceCancelRequired || job.State == "printing" || job.State == "releasing"
		job.State = "canceling"
	}
	err = s.save(ctx, &job)
	return job, err
}

func (s *Service) ConfirmCancel(ctx context.Context, owner, id string, deviceStopped bool) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.Job(ctx, owner, id)
	if err != nil {
		return Job{}, err
	}
	if !deviceStopped {
		return Job{}, ErrConflict
	}
	if job.Terminal() {
		return job, nil
	}
	if s.deviceCancel != nil {
		// A client assertion must not bypass a configured device verification.
		return Job{}, ErrConflict
	}
	if job.State != "canceling" || !job.DeviceCancelRequired || job.CUPSID == 0 {
		return Job{}, ErrConflict
	}
	status, err := s.backend.Status(ctx, job.CUPSID)
	if err != nil {
		return Job{}, ErrBackend
	}
	if status.Name != JobName(job) || status.State < 7 {
		return Job{}, ErrConflict
	}
	if problem := s.printerReady(ctx, job.Settings.PrinterID); problem != "" {
		return Job{}, ErrConflict
	}
	job.State = "canceled"
	if status.State == 8 || job.Problem == "settings_rejected" {
		job.State = "failed"
	}
	job.Problem = ""
	job.DeviceCancelRequired = false
	err = s.save(ctx, &job)
	return job, err
}

// An IPP terminal job can still own a live backend. Only idle is positive
// evidence that CUPS has released the printer; it says nothing about device buffers.
func (s *Service) printerReady(ctx context.Context, id string) string {
	list, err := s.backend.List(ctx)
	if err != nil {
		return "cups_unavailable"
	}
	for _, printer := range list {
		if printer.ID != id {
			continue
		}
		if printer.DeviceState == "disconnected" || printer.State == "stopped" {
			return "printer_stopped"
		}
		if printer.State == "processing" {
			return "printer_busy"
		}
		if printer.State == "idle" {
			return ""
		}
	}
	return "cups_unavailable"
}
func (s *Service) save(ctx context.Context, job *Job) error {
	job.UpdatedAt = time.Now().UTC()
	return s.repo.SaveJob(ctx, *job)
}

func (s *Service) expire(ctx context.Context, job *Job) error {
	if job.Terminal() || job.State == "canceling" || time.Now().Before(job.CreatedAt.Add(taskRetention)) {
		return nil
	}
	if job.State == "queued" || job.State == "awaiting_flip" {
		job.State = "canceled"
		job.Problem = ""
	} else {
		job.DeviceCancelRequired = job.DeviceCancelRequired || job.State == "printing" || job.State == "releasing"
		job.State = "canceling"
	}
	return s.save(ctx, job)
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) Tick(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.repo.ListJobs(ctx)
	if err != nil {
		return
	}
	// Recover an existing reservation before considering a restored backlog.
	sortJobs(all)
	busy := map[string]bool{}
	for _, job := range all {
		if job.Terminal() {
			continue
		}
		if err := s.expire(ctx, &job); err != nil || job.Terminal() || busy[job.Settings.PrinterID] {
			continue
		}
		busy[job.Settings.PrinterID] = true
		if job.State == "awaiting_flip" {
			continue
		}
		s.advance(ctx, &job)
	}
}

func JobName(job Job) string { return "PrintBridge-" + job.ID + "-" + job.Phase }

func (s *Service) advance(ctx context.Context, job *Job) {
	if ctx.Err() != nil {
		return
	}
	if job.State == "queued" {
		if problem := s.printerReady(ctx, job.Settings.PrinterID); problem != "" {
			job.Problem = problem
			_ = s.save(ctx, job)
			return
		}
		job.State = "creating"
		if s.save(ctx, job) != nil {
			return
		}
		id, err := s.backend.CreateHeld(ctx, *job)
		if err != nil {
			if errors.Is(err, ErrSettings) {
				job.State = "failed"
				job.Problem = "settings_rejected"
				if id > 0 {
					job.CUPSID = id
					job.CUPSJobIDs = append(job.CUPSJobIDs, id)
					job.State = "canceling"
				}
				_ = s.save(ctx, job)
				return
			}
			job.Problem = "submission_uncertain"
			_ = s.save(ctx, job)
			return
		}
		job.CUPSID = id
		job.CUPSJobIDs = append(job.CUPSJobIDs, id)
		job.State = "created"
		if s.save(ctx, job) != nil {
			return
		}
	}
	if job.CUPSID == 0 {
		matches, err := s.backend.Find(ctx, job.Settings.PrinterID, JobName(*job))
		if err != nil {
			job.Problem = "cups_unavailable"
			_ = s.save(ctx, job)
			return
		}
		if len(matches) != 1 {
			job.Problem = "submission_uncertain"
			_ = s.save(ctx, job)
			return
		}
		job.CUPSID = matches[0].ID
		job.CUPSJobIDs = append(job.CUPSJobIDs, job.CUPSID)
		if s.save(ctx, job) != nil {
			return
		}
	}
	status, err := s.backend.Status(ctx, job.CUPSID)
	if err != nil {
		job.Problem = "cups_unavailable"
		_ = s.save(ctx, job)
		return
	}
	if status.Name != JobName(*job) {
		job.Problem = "submission_uncertain"
		_ = s.save(ctx, job)
		return
	}
	job.CUPSState = status.State
	if status.State >= 7 && (status.State != 9 || job.State == "canceling") {
		// Canceled/aborted data may already be in USB or the device. Persist the
		// barrier across restarts and do not discard files before confirmation.
		job.DeviceCancelRequired = job.DeviceCancelRequired ||
			((status.Documents > 0 || job.State == "printing" || job.State == "releasing") &&
				job.Problem != "settings_rejected")
		if problem := s.printerReady(ctx, job.Settings.PrinterID); problem != "" {
			job.State = "canceling"
			if job.Problem != "settings_rejected" {
				job.Problem = problem
			}
			_ = s.save(ctx, job)
			return
		}
		if job.DeviceCancelRequired {
			job.State = "canceling"
			job.Problem = "device_cancel_required"
			if s.save(ctx, job) != nil || s.deviceCancel == nil {
				return
			}
			if err := s.deviceCancel.CancelDevice(ctx, job.Settings.PrinterID); err != nil {
				job.Problem = "device_cancel_failed"
				_ = s.save(ctx, job)
				return
			}
			job.DeviceCancelRequired = false
		}
		job.State = "canceled"
		if status.State == 8 || job.Problem == "settings_rejected" {
			job.State = "failed"
		} else {
			job.Problem = ""
		}
		_ = s.save(ctx, job)
		return
	}
	if status.State == 9 {
		if job.Settings.Manual() && job.Phase == job.Settings.FirstPhase() {
			if job.State == "canceling" {
				job.State = "canceled"
			} else {
				job.State = "awaiting_flip"
			}
		} else {
			job.State = "completed"
		}
		job.Problem = ""
		_ = s.save(ctx, job)
		return
	}
	if status.State == 7 || status.State == 8 {
		if status.State == 7 {
			job.State = "canceled"
		} else {
			job.State = "failed"
		}
		if job.Problem == "settings_rejected" {
			job.State = "failed"
		} else {
			job.Problem = ""
		}
		_ = s.save(ctx, job)
		return
	}
	if job.State == "canceling" {
		if err := s.backend.Cancel(ctx, job.CUPSID); err != nil {
			if job.Problem != "settings_rejected" {
				job.Problem = "cancel_uncertain"
			}
		} else if job.Problem != "settings_rejected" {
			job.Problem = ""
		}
		_ = s.save(ctx, job)
		return
	}
	if job.State == "creating" || job.State == "created" {
		if status.State != 4 || status.Hold != "indefinite" || status.Documents != 0 {
			job.Problem = "submission_uncertain"
			_ = s.save(ctx, job)
			return
		}
		job.State = "sending"
		if s.save(ctx, job) != nil {
			return
		}
		if err := s.backend.Send(ctx, job.CUPSID, filepath.Join(s.root, "jobs", job.ID, job.Phase+".pdf")); err != nil {
			job.Problem = "submission_uncertain"
			_ = s.save(ctx, job)
			return
		}
		job.State = "releasing"
		if s.save(ctx, job) != nil {
			return
		}
		// Re-read the scheduler rather than inferring receipt from HTTP alone.
		status, err = s.backend.Status(ctx, job.CUPSID)
		if err != nil {
			job.Problem = "cups_unavailable"
			_ = s.save(ctx, job)
			return
		}
	}
	if job.State == "sending" {
		if status.Documents != 1 {
			job.Problem = "submission_uncertain"
			_ = s.save(ctx, job)
			return
		}
		job.State = "releasing"
		if s.save(ctx, job) != nil {
			return
		}
	}
	if job.State == "releasing" {
		if status.State == 4 {
			if status.Hold != "indefinite" || status.Documents != 1 {
				job.Problem = "submission_uncertain"
				_ = s.save(ctx, job)
				return
			}
			if problem := s.printerReady(ctx, job.Settings.PrinterID); problem != "" {
				job.Problem = problem
				_ = s.save(ctx, job)
				return
			}
			if err := s.backend.Release(ctx, job.CUPSID); err != nil {
				job.Problem = "submission_uncertain"
				_ = s.save(ctx, job)
				return
			}
			// The held snapshot is no longer current after Release.
			job.CUPSState = 0
		}
		job.State = "printing"
	}
	job.Problem = ""
	if status.State == 6 {
		job.Problem = "printer_stopped"
	}
	_ = s.save(ctx, job)
}

func (s *Service) Cleanup(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.acquire(); err != nil {
		return nil
	}
	defer func() { <-s.gate }()
	livePlans := map[string]bool{}
	plans, err := s.repo.ListPlans(ctx)
	if err != nil {
		return err
	}
	jobs, err := s.repo.ListJobs(ctx)
	if err != nil {
		return err
	}
	type documentRef struct{ owner, id string }
	byPlan := map[string]Plan{}
	for _, plan := range plans {
		byPlan[plan.ID] = plan
	}
	finishedDocs, activeDocs := map[documentRef]bool{}, map[documentRef]bool{}
	finishedPlans, activePlans := map[string]bool{}, map[string]bool{}
	for i := range jobs {
		job := &jobs[i]
		if !recordID.MatchString(job.ID) {
			return ErrNotFound
		}
		if err := s.expire(ctx, job); err != nil {
			return err
		}
		if job.Terminal() {
			finishedPlans[job.PlanID] = true
		} else {
			activePlans[job.PlanID] = true
		}
		if plan, ok := byPlan[job.PlanID]; ok {
			ref := documentRef{plan.Owner, plan.DocumentID}
			if job.Terminal() {
				finishedDocs[ref] = true
			} else {
				activeDocs[ref] = true
			}
		}
	}
	for ref := range finishedDocs {
		if !activeDocs[ref] {
			if err := s.docs.Discard(ctx, ref.owner, ref.id); err != nil {
				return err
			}
		}
	}
	for _, plan := range plans {
		if !recordID.MatchString(plan.ID) {
			return ErrNotFound
		}
		ref := documentRef{plan.Owner, plan.DocumentID}
		finished := finishedPlans[plan.ID] && !activePlans[plan.ID] || finishedDocs[ref] && !activeDocs[ref]
		if finished || !time.Now().Before(plan.ExpiresAt) {
			if err := os.RemoveAll(filepath.Join(s.root, "plans", plan.ID)); err != nil {
				return err
			}
			if err := s.repo.DeletePlan(ctx, plan.ID); err != nil {
				return err
			}
		} else {
			livePlans[plan.ID] = true
		}
	}
	liveJobs := map[string]bool{}
	for _, job := range jobs {
		if !recordID.MatchString(job.ID) {
			return ErrNotFound
		}
		if job.Terminal() {
			if err := os.RemoveAll(filepath.Join(s.root, "jobs", job.ID)); err != nil {
				return err
			}
			if time.Since(job.UpdatedAt) >= taskRetention {
				if err := s.repo.DeleteJob(ctx, job.ID); err != nil {
					return err
				}
			}
		} else {
			liveJobs[job.ID] = true
		}
	}
	for _, set := range []struct {
		dir  string
		live map[string]bool
	}{{"plans", livePlans}, {"jobs", liveJobs}} {
		entries, err := os.ReadDir(filepath.Join(s.root, set.dir))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if recordID.MatchString(entry.Name()) && !set.live[entry.Name()] {
				if err := os.RemoveAll(filepath.Join(s.root, set.dir, entry.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) checkSpace(reserve int64) error {
	var used int64
	err := filepath.WalkDir(s.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symbolic links in print storage")
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			used += info.Size()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if used+reserve > 2<<30 {
		return ErrCapacity
	}
	return nil
}
