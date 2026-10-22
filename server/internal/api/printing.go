package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

type PrintService interface {
	Prepare(context.Context, string, string, printing.Settings, string) (printing.Plan, error)
	Plan(context.Context, string, string) (printing.Plan, error)
	Preview(context.Context, string, string, int, string) ([]byte, error)
	Create(context.Context, string, string, string) (printing.Job, error)
	Job(context.Context, string, string) (printing.Job, error)
	Jobs(context.Context, string, bool) ([]printing.Job, error)
	Continue(context.Context, string, string, bool) (printing.Job, error)
	Cancel(context.Context, string, string) (printing.Job, error)
	ConfirmCancel(context.Context, string, string, bool) (printing.Job, error)
}

func decodeInput(w http.ResponseWriter, r *http.Request, value any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, r, 415, "invalid_content_type", "application/json is required")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, r, 400, "invalid_request", "invalid request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, r, 400, "invalid_request", "one JSON object is required")
		return false
	}
	return true
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	writeError(w, r, 405, "method_not_allowed", "method not allowed")
	return false
}
func registerPrinting(mux *http.ServeMux, sessions *pairing.Service, service PrintService) {
	mux.HandleFunc("/api/v1/plans", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, "POST") {
			return
		}
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		var input struct {
			DocumentID     string            `json:"documentId"`
			Settings       printing.Settings `json:"settings"`
			ReplacesPlanID string            `json:"replacesPlanId"`
		}
		if !decodeInput(w, r, &input) {
			return
		}
		plan, err := service.Prepare(r.Context(), owner, input.DocumentID, input.Settings, input.ReplacesPlanID)
		if err != nil {
			printError(w, r, err)
			return
		}
		if _, ok := authorize(w, r, sessions); !ok {
			return
		}
		writeJSON(w, r, 201, plan)
	})
	mux.HandleFunc("/api/v1/plans/{id}", readOnly(func(w http.ResponseWriter, r *http.Request) {
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		plan, err := service.Plan(r.Context(), owner, r.PathValue("id"))
		if err != nil {
			printError(w, r, err)
			return
		}
		writeJSON(w, r, 200, plan)
	}))
	mux.HandleFunc("/api/v1/plans/{id}/pages/{page}", readOnly(func(w http.ResponseWriter, r *http.Request) {
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		page, err := strconv.Atoi(r.PathValue("page"))
		if err != nil {
			printError(w, r, documents.ErrPage)
			return
		}
		order := r.URL.Query().Get("order")
		if order != "" && order != "print" && order != "reading" {
			writeError(w, r, 400, "invalid_request", "order must be print or reading")
			return
		}
		data, err := service.Preview(r.Context(), owner, r.PathValue("id"), page, order)
		if err != nil {
			printError(w, r, err)
			return
		}
		if _, ok := authorize(w, r, sessions); !ok {
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(200)
		if r.Method != "HEAD" {
			_, _ = w.Write(data)
		}
	}))
	mux.HandleFunc("/api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "POST" {
			w.Header().Set("Allow", "GET, HEAD, POST")
			writeError(w, r, 405, "method_not_allowed", "method not allowed")
			return
		}
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		if r.Method != "POST" {
			active := r.URL.Query().Get("active")
			if active != "" && active != "true" && active != "false" {
				writeError(w, r, 400, "invalid_request", "active must be true or false")
				return
			}
			jobs, err := service.Jobs(r.Context(), owner, active == "true")
			if err != nil {
				printError(w, r, err)
				return
			}
			writeJSON(w, r, 200, struct {
				Jobs []printing.Job `json:"jobs"`
			}{jobs})
			return
		}
		var input struct {
			PlanID string `json:"planId"`
			Key    string `json:"idempotencyKey"`
		}
		if !decodeInput(w, r, &input) {
			return
		}
		job, err := service.Create(r.Context(), owner, input.PlanID, input.Key)
		if err != nil {
			printError(w, r, err)
			return
		}
		writeJSON(w, r, 201, job)
	})
	mux.HandleFunc("/api/v1/jobs/{id}", readOnly(func(w http.ResponseWriter, r *http.Request) {
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		job, err := service.Job(r.Context(), owner, r.PathValue("id"))
		if err != nil {
			printError(w, r, err)
			return
		}
		writeJSON(w, r, 200, job)
	}))
	for _, action := range []string{"cancel", "continue", "confirm-cancel"} {
		mux.HandleFunc("/api/v1/jobs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			if !method(w, r, "POST") {
				return
			}
			owner, ok := authorize(w, r, sessions)
			if !ok {
				return
			}
			var job printing.Job
			var err error
			if action == "continue" {
				var input struct {
					PaperConfirmed bool `json:"paperConfirmed"`
				}
				if !decodeInput(w, r, &input) {
					return
				}
				job, err = service.Continue(r.Context(), owner, r.PathValue("id"), input.PaperConfirmed)
			} else if action == "confirm-cancel" {
				var input struct {
					DeviceStopped bool `json:"deviceStopped"`
				}
				if !decodeInput(w, r, &input) {
					return
				}
				job, err = service.ConfirmCancel(r.Context(), owner, r.PathValue("id"), input.DeviceStopped)
			} else {
				job, err = service.Cancel(r.Context(), owner, r.PathValue("id"))
			}
			if err != nil {
				printError(w, r, err)
				return
			}
			writeJSON(w, r, 200, job)
		})
	}
}
func printError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, accounts.ErrPaused):
		accountError(w, r, err)
	case errors.Is(err, printing.ErrSettings):
		writeError(w, r, 400, "invalid_settings", "invalid or unsupported print settings")
	case errors.Is(err, printing.ErrNotFound):
		writeError(w, r, 404, "print_not_found", "print record is missing, expired or belongs to another session")
	case errors.Is(err, printing.ErrConflict):
		writeError(w, r, 409, "print_conflict", "action conflicts with current task state")
	case errors.Is(err, printing.ErrActiveJob):
		writeError(w, r, 409, "active_print_exists", "continue or cancel the unfinished job on this printer")
	case errors.Is(err, printing.ErrBusy):
		w.Header().Set("Retry-After", "2")
		writeError(w, r, 503, "print_busy", "print preparation is busy")
	case errors.Is(err, printing.ErrCapacity):
		writeError(w, r, 507, "print_capacity", "print capacity reached")
	case errors.Is(err, printing.ErrBackend):
		writeError(w, r, 503, "cups_unavailable", "CUPS is unavailable")
	case errors.Is(err, printing.ErrWatermarkFont):
		writeError(w, r, 422, "watermark_font_unavailable", "watermark font is missing or does not contain the required characters")
	default:
		documentError(w, r, err)
	}
}
