package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/RayFlows/printbridge/server/internal/app"
	"github.com/RayFlows/printbridge/server/internal/config"
	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/pdf"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

type virtualPrintCase struct {
	sides, outputOrder, backOrder, firstSide string
	profile, orientation                     string
	secondRotation                           int
	nup, copies                              int
	sheets                                   []printing.Sheet
	layout                                   [][]int
	phases                                   [][][]int
	effects                                  bool
	source                                   string
}

func TestDocumentEffectsAgainstVirtualScheduler(t *testing.T) {
	t.Run("document mirror watermark and restart", func(t *testing.T) {
		runVirtualPrintFlow(t, virtualPrintCase{
			effects: true, sides: "manual-long-edge", outputOrder: "reverse", backOrder: "same", firstSide: "back", nup: 1, copies: 1,
			sheets: []printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}},
			layout: [][]int{{3}, {}, {1}, {2}}, phases: [][][]int{{{}, {2}}, {{3}, {1}}},
		})
	})
	t.Run("image inversion with blank first pass", func(t *testing.T) {
		runVirtualPrintFlow(t, virtualPrintCase{
			effects: true, source: "sample.png", sides: "manual-long-edge", outputOrder: "reverse", backOrder: "same", firstSide: "back", nup: 1, copies: 1,
			sheets: []printing.Sheet{{Front: []int{1}, Back: []int{0}}},
			layout: [][]int{{1}, {}}, phases: [][][]int{{{}}, {{1}}},
		})
	})
}

func TestConfiguredReloadProfileAgainstVirtualScheduler(t *testing.T) {
	for _, tc := range []struct {
		orientation, sides string
		rotation           int
	}{
		{"portrait", "manual-long-edge", 0},
		{"portrait", "manual-short-edge", 180},
		{"landscape", "manual-long-edge", 180},
		{"landscape", "manual-short-edge", 0},
	} {
		t.Run(tc.orientation+"/"+tc.sides, func(t *testing.T) {
			runVirtualPrintFlow(t, virtualPrintCase{
				profile: printing.RearFaceUpProfile, orientation: tc.orientation, secondRotation: tc.rotation,
				sides: tc.sides, outputOrder: "reverse", nup: 1, copies: 1,
				sheets: []printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}},
				layout: [][]int{{3}, {}, {1}, {2}},
				phases: [][][]int{{{}, {2}}, {{3}, {1}}},
			})
		})
	}
}

// This integration connects the real HTTP, SQLite, PDF and IPP adapters exclusively
// to a loopback scheduler which holds document bytes in memory and never prints.
func TestFullManualFlowAgainstVirtualScheduler(t *testing.T) {
	runVirtualPrintFlow(t, virtualPrintCase{
		sides: "manual-long-edge", backOrder: "same", nup: 1, copies: 1,
		sheets: []printing.Sheet{{Front: []int{1}, Back: []int{2}}, {Front: []int{3}, Back: []int{0}}},
		layout: [][]int{{1}, {2}, {3}, {}},
		phases: [][][]int{{{1}, {3}}, {{2}, {}}},
	})
}

func TestReverseSheetOutputAgainstVirtualScheduler(t *testing.T) {
	for _, tt := range []struct {
		name string
		virtualPrintCase
	}{
		{"simplex copies", virtualPrintCase{
			sides: "one-sided", outputOrder: "reverse", backOrder: "same", nup: 1, copies: 2,
			sheets: []printing.Sheet{{Front: []int{3}, Back: []int{}}, {Front: []int{2}, Back: []int{}}, {Front: []int{1}, Back: []int{}}},
			layout: [][]int{{3}, {2}, {1}},
			phases: [][][]int{{{3}, {2}, {1}, {3}, {2}, {1}}},
		}},
		{"simplex nup", virtualPrintCase{
			sides: "one-sided", outputOrder: "reverse", backOrder: "same", nup: 2, copies: 1,
			sheets: []printing.Sheet{{Front: []int{3, 0}, Back: []int{}}, {Front: []int{1, 2}, Back: []int{}}},
			layout: [][]int{{3}, {1, 2}},
			phases: [][][]int{{{3}, {1, 2}}},
		}},
		{"automatic duplex", virtualPrintCase{
			sides: "two-sided-long-edge", outputOrder: "reverse", backOrder: "same", nup: 1, copies: 1,
			sheets: []printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}},
			layout: [][]int{{3}, {}, {1}, {2}},
			phases: [][][]int{{{3}, {}, {1}, {2}}},
		}},
		{"manual duplex same back order", virtualPrintCase{
			sides: "manual-long-edge", outputOrder: "reverse", backOrder: "same", nup: 1, copies: 2,
			sheets: []printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}},
			layout: [][]int{{3}, {}, {1}, {2}},
			phases: [][][]int{{{3}, {1}, {3}, {1}}, {{}, {2}, {}, {2}}},
		}},
		{"manual short edge reverse back order", virtualPrintCase{
			sides: "manual-short-edge", outputOrder: "reverse", backOrder: "reverse", nup: 1, copies: 2,
			sheets: []printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}},
			layout: [][]int{{3}, {}, {1}, {2}},
			phases: [][][]int{{{3}, {1}, {3}, {1}}, {{2}, {}, {2}, {}}},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) { runVirtualPrintFlow(t, tt.virtualPrintCase) })
	}
}

func TestEvenFirstManualOutputAgainstVirtualScheduler(t *testing.T) {
	for _, tc := range []struct {
		name string
		virtualPrintCase
	}{
		{"three pages", virtualPrintCase{
			sides: "manual-long-edge", outputOrder: "reverse", backOrder: "same", firstSide: "back", nup: 1, copies: 1,
			sheets: []printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}},
			layout: [][]int{{3}, {}, {1}, {2}},
			phases: [][][]int{{{}, {2}}, {{3}, {1}}},
		}},
		{"short edge copies reverse second pass", virtualPrintCase{
			sides: "manual-short-edge", outputOrder: "reverse", backOrder: "reverse", firstSide: "back", nup: 1, copies: 2,
			sheets: []printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}},
			layout: [][]int{{3}, {}, {1}, {2}},
			phases: [][][]int{{{}, {2}, {}, {2}}, {{1}, {3}, {1}, {3}}},
		}},
		{"two-up", virtualPrintCase{
			sides: "manual-long-edge", outputOrder: "reverse", backOrder: "same", firstSide: "back", nup: 2, copies: 1,
			sheets: []printing.Sheet{{Front: []int{1, 2}, Back: []int{3, 0}}},
			layout: [][]int{{1, 2}, {3}},
			phases: [][][]int{{{3}}, {{1, 2}}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { runVirtualPrintFlow(t, tc.virtualPrintCase) })
	}
}

func assertPDFPageText(t *testing.T, path string, want [][]int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	text, err := exec.CommandContext(ctx, "pdftotext", "-layout", path, "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	pages := strings.Split(strings.TrimSuffix(string(text), "\f"), "\f")
	got := make([][]int, len(pages))
	marker := regexp.MustCompile(`\bPage ([0-9]+)\b`)
	for i, page := range pages {
		got[i] = []int{}
		for _, match := range marker.FindAllStringSubmatch(page, -1) {
			n, err := strconv.Atoi(match[1])
			if err != nil {
				t.Fatal(err)
			}
			got[i] = append(got[i], n)
		}
		if len(got[i]) == 0 && strings.TrimSpace(page) != "" {
			t.Fatalf("unexpected nonblank face %d", i+1)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: PDF faces=%v, want=%v", filepath.Base(path), got, want)
	}
}

func runVirtualPrintFlow(t *testing.T, tc virtualPrintCase) {
	t.Helper()
	if os.Getenv("PRINTBRIDGE_TEST_POPPLER") != "1" {
		t.Skip("requires Poppler on PATH; no real printer is used")
	}
	var mu sync.Mutex
	jobs := map[int]printing.Status{}
	payloads := map[int][]byte{}
	integer := func(name string, n int) goipp.Attribute {
		return goipp.MakeAttribute(name, goipp.TagInteger, goipp.Integer(n))
	}
	text := func(name string, tag goipp.Tag, v string) goipp.Attribute {
		return goipp.MakeAttribute(name, tag, goipp.String(v))
	}
	first := func(attrs goipp.Attributes, name string) string {
		for _, a := range attrs {
			if a.Name == name && len(a.Values) > 0 {
				return a.Values[0].V.String()
			}
		}
		return ""
	}
	virtual := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var request goipp.Message
		if err := request.Decode(r.Body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		response := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, request.RequestID)
		target, _ := url.Parse(first(request.Operation, "job-uri"))
		id, _ := strconv.Atoi(strings.TrimPrefix(target.Path, "/jobs/"))
		switch goipp.Op(request.Code) {
		case goipp.OpCupsGetPrinters:
			sides := text("sides-supported", goipp.TagKeyword, "one-sided")
			sides.Values.Add(goipp.TagKeyword, goipp.String("two-sided-long-edge"))
			response.Printer = goipp.Attributes{text("printer-name", goipp.TagName, "virtual"),
				goipp.MakeAttribute("printer-state", goipp.TagEnum, goipp.Integer(3)),
				text("media-supported", goipp.TagKeyword, "iso_a4_210x297mm"), sides}
		case goipp.OpCreateJob:
			for name, want := range map[string]string{"page-set": "all", "output-order": "normal", "number-up": "1", "copies": "1"} {
				if first(request.Job, name) != want {
					t.Error("prepared PDF was filtered or reordered again:", name)
				}
			}
			for _, name := range []string{"page-ranges", "number-up-layout", "scaling"} {
				if first(request.Job, name) != "" {
					t.Error("prepared PDF was filtered or reordered again:", name)
				}
			}
			id = len(jobs) + 1
			jobs[id] = printing.Status{ID: id, Name: first(request.Operation, "job-name"), State: 4, Hold: first(request.Job, "job-hold-until")}
			response.Job.Add(integer("job-id", id))
		case goipp.OpSendDocument:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if !bytes.HasPrefix(data, []byte("%PDF-")) {
				t.Error("non-PDF phase")
			}
			if _, duplicate := payloads[id]; duplicate {
				t.Error("document resent")
			}
			payloads[id] = data
			job := jobs[id]
			job.Documents = 1
			jobs[id] = job
		case goipp.OpReleaseJob:
			job := jobs[id]
			if job.Documents != 1 || job.Hold != "indefinite" {
				t.Error("released before receiving held document")
			}
			job.State = 9
			jobs[id] = job
		case goipp.OpGetJobAttributes:
			job, ok := jobs[id]
			if !ok {
				response.Code = goipp.Code(goipp.StatusErrorNotFound)
			} else {
				response.Job = goipp.Attributes{integer("job-id", id), text("job-name", goipp.TagName, job.Name),
					goipp.MakeAttribute("job-state", goipp.TagEnum, goipp.Integer(job.State)),
					integer("number-of-documents", job.Documents), text("job-hold-until", goipp.TagKeyword, job.Hold)}
			}
		default:
			t.Error("unexpected virtual scheduler operation", goipp.Op(request.Code))
		}
		w.Header().Set("Content-Type", goipp.ContentType)
		if err := response.Encode(w); err != nil {
			t.Error(err)
		}
	}))
	defer virtual.Close()
	cfg, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	cfg.CUPSURL = virtual.URL
	cfg.DataDir = t.TempDir()
	cfg.PairingCode = "test-only-pairing-code-0123456789012345"
	if tc.profile != "" {
		cfg.ManualDuplexProfiles = map[string]string{"virtual": tc.profile}
	}
	handler, err := app.NewHandler(cfg, "integration")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer func() { server.Close(); handler.Close() }()
	client := &http.Client{Timeout: 30 * time.Second}
	token := ""
	call := func(method, path string, body []byte, contentType string, status int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", contentType)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			t.Fatalf("%s %s: status %d: %s", method, path, resp.StatusCode, data)
		}
		return data
	}
	var session pairing.Session
	data := call("POST", "/api/v1/sessions", []byte(`{"pairingCode":"`+cfg.PairingCode+`"}`), "application/json", 201)
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	token = session.Token
	if tc.profile != "" {
		data := call("GET", "/api/v1/printers", nil, "", 200)
		if !bytes.Contains(data, []byte(`"manualDuplexProfile":"`+tc.profile+`"`)) {
			t.Fatal("configured profile not advertised")
		}
	}
	var multipartBody bytes.Buffer
	form := multipart.NewWriter(&multipartBody)
	sourceName := tc.source
	if sourceName == "" {
		sourceName = "three-pages.pdf"
	}
	part, _ := form.CreateFormFile("file", sourceName)
	source, err := os.ReadFile(filepath.Join("..", "pdf", "testdata", sourceName))
	if err != nil {
		t.Fatal(err)
	}
	part.Write(source)
	form.Close()
	var doc documents.Document
	data = call("POST", "/api/v1/documents", multipartBody.Bytes(), form.FormDataContentType(), 201)
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	settings := printing.Settings{PrinterID: "virtual", Media: "iso_a4_210x297mm", Copies: tc.copies, Subset: "all",
		Orientation: "portrait", PagesPerSheet: tc.nup, ScalePercent: 100, Scaling: "fit",
		Sides: tc.sides, BackOrder: tc.backOrder, Calibrated: true, OutputOrder: tc.outputOrder, ManualFirstSide: tc.firstSide}
	if tc.profile != "" {
		settings.ManualDuplexProfile = tc.profile
		settings.Calibrated = false
		settings.Orientation = tc.orientation
	}
	if tc.effects {
		settings.Mirror, settings.Watermark = true, "PrintBridge / draft"
		settings.InvertImages = tc.source != ""
	}
	input, _ := json.Marshal(map[string]any{"documentId": doc.ID, "settings": settings})
	var plan printing.Plan
	data = call("POST", "/api/v1/plans", input, "application/json", 201)
	if err := json.Unmarshal(data, &plan); err != nil || plan.PageCount != len(tc.layout) ||
		plan.SheetCount != len(tc.sheets)*tc.copies || !reflect.DeepEqual(plan.Sheets, tc.sheets) {
		t.Fatal(plan, err)
	}
	if tc.profile != "" && (plan.Settings.ManualDuplexProfile != tc.profile || plan.Settings.FirstPhase() != "back" ||
		plan.Settings.BackOrder != "same" || !plan.Settings.Calibrated) {
		t.Fatal("profile not resolved in persisted plan", plan.Settings)
	}
	previews := map[int][]byte{}
	if !tc.effects {
		assertPDFPageText(t, filepath.Join(cfg.DataDir, "printing", "plans", plan.ID, "layout.pdf"), tc.layout)
	} else {
		if plan.Settings.Mirror != settings.Mirror || plan.Settings.Watermark != settings.Watermark ||
			plan.Settings.InvertImages != settings.InvertImages {
			t.Fatal("effect settings lost")
		}
		for page := 1; page <= plan.PageCount; page++ {
			previews[page] = call("GET", "/api/v1/plans/"+plan.ID+"/pages/"+strconv.Itoa(page), nil, "", 200)
		}
	}
	data = call("GET", "/api/v1/plans/"+plan.ID+"/pages/"+strconv.Itoa(plan.PageCount), nil, "", 200)
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	readingCount := (len(plan.SelectedPages) + settings.PagesPerSheet - 1) / settings.PagesPerSheet
	if plan.PreviewPageCount != readingCount {
		t.Fatal("reading count includes duplex padding", plan)
	}
	_, faces := printing.ArrangeSheets(plan.SelectedPages, plan.Settings)
	for page := 1; page <= readingCount; page++ {
		physical := 0
		for i, original := range faces {
			if original == page {
				physical = i + 1
			}
		}
		reading := call("GET", "/api/v1/plans/"+plan.ID+"/pages/"+strconv.Itoa(page)+"?order=reading", nil, "", 200)
		printed := call("GET", "/api/v1/plans/"+plan.ID+"/pages/"+strconv.Itoa(physical), nil, "", 200)
		if !bytes.Equal(reading, printed) {
			t.Fatal("reading preview differs from the prepared print face", page, physical)
		}
	}
	call("GET", "/api/v1/plans/"+plan.ID+"/pages/"+strconv.Itoa(readingCount+1)+"?order=reading", nil, "", 400)
	mu.Lock()
	created := len(jobs)
	mu.Unlock()
	if created != 0 {
		t.Fatal("upload/preview printed")
	}
	input, _ = json.Marshal(map[string]string{"planId": plan.ID, "idempotencyKey": "request-0000000001"})
	var job printing.Job
	data = call("POST", "/api/v1/jobs", input, "application/json", 201)
	json.Unmarshal(data, &job)
	data = call("POST", "/api/v1/jobs", input, "application/json", 201)
	var retry printing.Job
	json.Unmarshal(data, &retry)
	if job.ID != retry.ID {
		t.Fatal("duplicate job")
	}
	waitFor := func(state string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			data := call("GET", "/api/v1/jobs/"+job.ID, nil, "", 200)
			json.Unmarshal(data, &job)
			if job.State == state {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("did not reach %s: %+v", state, job)
	}
	if settings.Manual() {
		waitFor("awaiting_flip")
		server.Close()
		if err := handler.Close(); err != nil {
			t.Fatal(err)
		}
		handler, err = app.NewHandler(cfg, "integration-restart")
		if err != nil {
			t.Fatal(err)
		}
		server = httptest.NewServer(handler)
		call("POST", "/api/v1/jobs/"+job.ID+"/continue", []byte(`{"paperConfirmed":false}`), "application/json", 409)
		call("POST", "/api/v1/jobs/"+job.ID+"/continue", []byte(`{"paperConfirmed":true}`), "application/json", 200)
	}
	waitFor("completed")
	mu.Lock()
	defer mu.Unlock()
	if len(jobs) != len(tc.phases) || len(payloads) != len(tc.phases) {
		t.Fatal("wrong physical phase count", len(jobs), len(payloads))
	}
	if len(job.CUPSJobIDs) != len(tc.phases) {
		t.Fatal("phase IDs not persisted")
	}
	for i, phase := range tc.phases {
		output := filepath.Join(t.TempDir(), "sent.pdf")
		if err := os.WriteFile(output, payloads[job.CUPSJobIDs[i]], 0600); err != nil {
			t.Fatal(err)
		}
		if !tc.effects {
			assertPDFPageText(t, output, phase)
		} else {
			phaseName := "single"
			if settings.Manual() {
				phaseName = settings.FirstPhase()
				if i == 1 {
					phaseName = settings.SecondPhase()
				}
			}
			pages := printing.PhasePages(plan.PageCount, settings, phaseName)
			processor := pdf.New("pdfinfo", "pdftoppm", 30*time.Second)
			for page, face := range pages {
				rendered := filepath.Join(t.TempDir(), "submitted.png")
				if err := processor.Render(context.Background(), output, page+1, rendered); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(rendered)
				if err != nil || !bytes.Equal(data, previews[face]) {
					t.Fatal("submitted PDF differs from preview", page+1, face, err)
				}
			}
			if job.Settings.Mirror != settings.Mirror || job.Settings.Watermark != settings.Watermark ||
				job.Settings.InvertImages != settings.InvertImages {
				t.Fatal("job effects changed after restart")
			}
		}
		if tc.profile != "" {
			info, err := exec.Command("pdfinfo", "-f", "1", "-l", "1", output).Output()
			want := 0
			if i == 1 {
				want = tc.secondRotation
			}
			pattern := regexp.MustCompile(`(?m)^Page\s+1 rot:\s+` + strconv.Itoa(want) + `\s*$`)
			if err != nil || !pattern.Match(info) {
				t.Fatalf("actual submitted PDF rotation differs: %s, %v", info, err)
			}
		}
	}
}
