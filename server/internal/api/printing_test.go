package api_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/api"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

type printService struct {
	calls    int
	owner    string
	settings printing.Settings
	replaces string
	active   bool
	order    string
	err      error
}

func (p *printService) Prepare(_ context.Context, owner, _ string, s printing.Settings, replaces string) (printing.Plan, error) {
	p.calls++
	p.owner = owner
	p.settings = s
	p.replaces = replaces
	return printing.Plan{ID: strings.Repeat("a", 32), Settings: s}, p.err
}
func (p *printService) Plan(context.Context, string, string) (printing.Plan, error) {
	p.calls++
	return printing.Plan{}, p.err
}
func (p *printService) Preview(_ context.Context, _, _ string, _ int, order string) ([]byte, error) {
	p.calls++
	p.order = order
	var data bytes.Buffer
	png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 8, 12)))
	return data.Bytes(), p.err
}
func (p *printService) Create(context.Context, string, string, string) (printing.Job, error) {
	p.calls++
	return printing.Job{}, p.err
}
func (p *printService) Job(context.Context, string, string) (printing.Job, error) {
	p.calls++
	return printing.Job{}, p.err
}
func (p *printService) Jobs(_ context.Context, _ string, active bool) ([]printing.Job, error) {
	p.calls++
	p.active = active
	return []printing.Job{}, p.err
}
func (p *printService) Continue(_ context.Context, _, _ string, confirmed bool) (printing.Job, error) {
	p.calls++
	if !confirmed {
		return printing.Job{}, printing.ErrConflict
	}
	return printing.Job{State: "queued", Phase: "back"}, p.err
}
func (p *printService) Cancel(context.Context, string, string) (printing.Job, error) {
	p.calls++
	return printing.Job{State: "canceling"}, p.err
}
func (p *printService) ConfirmCancel(_ context.Context, owner, _ string, confirmed bool) (printing.Job, error) {
	p.calls++
	p.owner = owner
	if !confirmed {
		return printing.Job{}, printing.ErrConflict
	}
	return printing.Job{State: "canceled"}, p.err
}

func TestPrintingHTTPContractAndAuthorization(t *testing.T) {
	t.Parallel()
	sessions := pairing.New(code, time.Hour)
	session, _ := sessions.Pair(code)
	service := &printService{}
	handler := api.NewHandler("test", sessions, nil, nil, service)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	tests := []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/v1/plans", `{"documentId":"doc","replacesPlanId":"previous","settings":{"copies":2,"subset":"odd","mediaType":"stationery","mediaSource":"rear","layoutOrder":"tbrl","pageBorder":true,"scaling":"actual","outputOrder":"reverse","manualFirstSide":"back","mirror":true,"watermark":"内部资料","invertImages":true}}`, 201},
		{"GET", "/api/v1/plans/test", "", 200}, {"GET", "/api/v1/plans/test/pages/1", "", 200},
		{"HEAD", "/api/v1/plans/test/pages/1", "", 200},
		{"GET", "/api/v1/plans/test/pages/1?order=print", "", 200},
		{"GET", "/api/v1/plans/test/pages/1?order=reading", "", 200},
		{"POST", "/api/v1/jobs", `{"planId":"plan","idempotencyKey":"request-0000000001"}`, 201},
		{"GET", "/api/v1/jobs", "", 200}, {"GET", "/api/v1/jobs/test", "", 200},
		{"GET", "/api/v1/jobs?active=true", "", 200},
		{"POST", "/api/v1/jobs/test/continue", `{"paperConfirmed":true}`, 200},
		{"POST", "/api/v1/jobs/test/cancel", "{}", 200},
		{"POST", "/api/v1/jobs/test/confirm-cancel", `{"deviceStopped":true}`, 200},
	}
	for _, tt := range tests {
		before := service.calls
		if w := request(tt.method, tt.path, tt.body, ""); w.Code != 401 || service.calls != before {
			t.Fatal("unauthorized access", w.Code)
		}
		w := request(tt.method, tt.path, tt.body, session.Token)
		if w.Code != tt.status {
			t.Fatal(tt.path, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private result cacheable")
		}
		if tt.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
	}
	if !service.active {
		t.Fatal("active-only filter did not reach the business service")
	}
	before := service.calls
	if service.order != "reading" {
		t.Fatal("reading preview order did not reach the service")
	}
	if w := request("GET", "/api/v1/plans/test/pages/1?order=unknown", "", session.Token); w.Code != 400 || service.calls != before {
		t.Fatal("invalid preview order was accepted", w.Code)
	}
	if w := request("GET", "/api/v1/jobs?active=unknown", "", session.Token); w.Code != 400 || service.calls != before {
		t.Fatal("invalid active-only filter was accepted", w.Code)
	}
	if service.owner == session.Token || service.owner == "" || service.settings.Subset != "odd" {
		t.Fatal("protocol mapping incorrect")
	}
	if service.settings.MediaType != "stationery" || service.settings.MediaSource != "rear" ||
		service.settings.LayoutOrder != "tbrl" || !service.settings.PageBorder || service.settings.Scaling != "actual" ||
		service.settings.OutputOrder != "reverse" || service.settings.ManualFirstSide != "back" || service.replaces != "previous" ||
		!service.settings.Mirror || !service.settings.InvertImages || service.settings.Watermark != "内部资料" {
		t.Fatal("new settings did not reach the business service")
	}
	for _, body := range []string{`{"unknown":true}`, `{"documentId":"doc"}{}`, `{"settings":{"copies":"two"}}`} {
		if w := request("POST", "/api/v1/plans", body, session.Token); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if w := request("POST", "/api/v1/jobs/test/continue", `{"paperConfirmed":false}`, session.Token); w.Code != 409 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{}`, `{"deviceStopped":false}`, `{"deviceStopped":null}`} {
		if w := request("POST", "/api/v1/jobs/test/confirm-cancel", body, session.Token); w.Code != 409 {
			t.Fatal("device confirmation missing", w.Code)
		}
	}
	for _, body := range []string{`{"deviceStopped":"true"}`, `{"deviceStopped":true,"unknown":1}`, `{} {}`} {
		if w := request("POST", "/api/v1/jobs/test/confirm-cancel", body, session.Token); w.Code != 400 {
			t.Fatal("invalid device confirmation", w.Code)
		}
	}
	for _, tt := range []struct {
		err    error
		status int
	}{
		{printing.ErrSettings, 400}, {printing.ErrNotFound, 404}, {printing.ErrConflict, 409}, {printing.ErrBusy, 503},
		{printing.ErrActiveJob, 409},
		{printing.ErrCapacity, 507}, {printing.ErrBackend, 503}, {printing.ErrWatermarkFont, 422}, {errors.New("private diagnostic"), 500},
	} {
		service.err = tt.err
		w := request("GET", "/api/v1/jobs/test", "", session.Token)
		if w.Code != tt.status || strings.Contains(w.Body.String(), "private diagnostic") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
