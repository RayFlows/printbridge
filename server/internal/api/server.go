package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/management"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printers"
)

type serverInfo struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	APIVersion string `json:"apiVersion"`
	LoginMode  string `json:"loginMode,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error apiError `json:"error"`
}

type PrinterReader interface {
	List(context.Context) ([]printers.Printer, error)
}

func NewHandler(version string, sessions *pairing.Service, reader PrinterReader, docs DocumentService, printServices ...PrintService) http.Handler {
	var prints PrintService
	if len(printServices) > 0 {
		prints = printServices[0]
	}
	return NewManagedHandler(version, sessions, reader, docs, prints, nil)
}

func NewManagedHandler(version string, sessions *pairing.Service, reader PrinterReader, docs DocumentService,
	prints PrintService, manager *management.Service) http.Handler {
	mux := http.NewServeMux()
	mode := ""
	if sessions.Users() != nil {
		mode = "wechat"
	}
	mux.HandleFunc("/healthz", readOnly(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}))
	mux.HandleFunc("/api/v1/server", readOnly(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, http.StatusOK, serverInfo{
			Name:       "PrintBridge",
			Version:    version,
			APIVersion: "v1",
			LoginMode:  mode,
		})
	}))
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			writeError(w, r, http.StatusUnsupportedMediaType, "invalid_content_type", "application/json is required")
			return
		}
		var input struct {
			PairingCode string `json:"pairingCode"`
			LoginCode   string `json:"loginCode"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.PairingCode == "" || len(input.PairingCode) > 128 {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "a pairingCode of at most 128 characters is required")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "a single JSON object is required")
			return
		}
		session, err := sessions.Login(r.Context(), input.PairingCode, input.LoginCode)
		switch {
		case err == nil:
			writeJSON(w, r, http.StatusCreated, session)
		case errors.Is(err, pairing.ErrInvalidCode):
			writeError(w, r, http.StatusUnauthorized, "invalid_pairing_code", "pairing code is incorrect")
		case errors.Is(err, pairing.ErrDisabled):
			writeError(w, r, http.StatusServiceUnavailable, "pairing_disabled", "device pairing has not been configured")
		case errors.Is(err, pairing.ErrRateLimited):
			w.Header().Set("Retry-After", "60")
			writeError(w, r, http.StatusTooManyRequests, "pairing_rate_limited", "try pairing again in one minute")
		case errors.Is(err, pairing.ErrCapacity):
			writeError(w, r, http.StatusServiceUnavailable, "session_capacity", "device session capacity reached")
		default:
			accountError(w, r, err)
		}
	})
	mux.HandleFunc("/api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		_, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		if err := sessions.Revoke(bearer(r)); err != nil {
			writeError(w, r, 500, "internal_error", "session could not be revoked")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/v1/printers", readOnly(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authorize(w, r, sessions); !ok {
			return
		}
		list, err := reader.List(r.Context())
		if err != nil {
			writeError(w, r, http.StatusServiceUnavailable, "cups_unavailable", "CUPS printer information is unavailable")
			return
		}
		if list == nil {
			list = []printers.Printer{}
		}
		writeJSON(w, r, http.StatusOK, struct {
			Printers []printers.Printer `json:"printers"`
		}{list})
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "endpoint not found")
	})
	if docs != nil {
		registerDocuments(mux, sessions, docs)
	}
	if prints != nil {
		registerPrinting(mux, sessions, prints)
	}
	if sessions.Users() != nil {
		registerAccounts(mux, sessions, manager)
	}
	return mux
}

func authorize(w http.ResponseWriter, r *http.Request, sessions *pairing.Service) (string, bool) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 128 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, r, http.StatusUnauthorized, "session_expired", "pair with the device to continue")
		return "", false
	}
	owner, err := sessions.Owner(r.Context(), fields[1])
	if err != nil {
		if errors.Is(err, accounts.ErrBanned) {
			accountError(w, r, err)
			return "", false
		}
		if !errors.Is(err, pairing.ErrUnauthorized) && !errors.Is(err, accounts.ErrNotFound) {
			accountError(w, r, err)
			return "", false
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, r, http.StatusUnauthorized, "session_expired", "pair with the device to continue")
		return "", false
	}
	return owner, true
}

func bearer(r *http.Request) string {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) == 2 {
		return fields[1]
	}
	return ""
}

func readOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		next(w, r)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, r, status, errorResponse{Error: apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(body)
	}
}
