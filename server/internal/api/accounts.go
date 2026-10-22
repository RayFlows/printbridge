package api

import (
	"errors"
	"net/http"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/management"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

func registerAccounts(mux *http.ServeMux, sessions *pairing.Service, manager *management.Service) {
	users := sessions.Users()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "POST" {
			w.Header().Set("Allow", "GET, HEAD, POST")
			writeError(w, r, 405, "method_not_allowed", "method not allowed")
			return
		}
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		user, err := users.Get(r.Context(), owner)
		if r.Method == "POST" && err == nil {
			var input struct {
				Name *string `json:"name"`
			}
			if !decodeInput(w, r, &input) {
				return
			}
			if input.Name == nil {
				writeError(w, r, 400, "invalid_request", "name is required")
				return
			}
			user, err = users.Profile(r.Context(), owner, *input.Name)
		}
		if err != nil {
			accountError(w, r, err)
			return
		}
		settings, err := users.Settings(r.Context())
		if err != nil {
			accountError(w, r, err)
			return
		}
		writeJSON(w, r, 200, struct {
			User     accounts.User     `json:"user"`
			Settings accounts.Settings `json:"settings"`
		}{user, settings})
	})
	mux.HandleFunc("/api/v1/admin/users", readOnly(func(w http.ResponseWriter, r *http.Request) {
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		list, err := users.Users(r.Context(), owner)
		if err != nil {
			accountError(w, r, err)
			return
		}
		writeJSON(w, r, 200, struct {
			Users []accounts.User `json:"users"`
		}{list})
	}))
	mux.HandleFunc("/api/v1/admin/users/{id}/ban", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, "POST") {
			return
		}
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		var input struct {
			Banned *bool `json:"banned"`
		}
		if !decodeInput(w, r, &input) {
			return
		}
		if input.Banned == nil {
			writeError(w, r, 400, "invalid_request", "banned is required")
			return
		}
		user, err := users.Ban(r.Context(), owner, r.PathValue("id"), *input.Banned)
		if err != nil {
			accountError(w, r, err)
			return
		}
		writeJSON(w, r, 200, user)
	})
	mux.HandleFunc("/api/v1/admin/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "POST" {
			w.Header().Set("Allow", "GET, HEAD, POST")
			writeError(w, r, 405, "method_not_allowed", "method not allowed")
			return
		}
		owner, ok := authorize(w, r, sessions)
		if !ok {
			return
		}
		if err := users.RequireAdmin(r.Context(), owner); err != nil {
			accountError(w, r, err)
			return
		}
		settings, err := users.Settings(r.Context())
		if r.Method == "POST" && err == nil {
			var input struct {
				PrintingEnabled *bool `json:"printingEnabled"`
			}
			if !decodeInput(w, r, &input) {
				return
			}
			if input.PrintingEnabled == nil {
				writeError(w, r, 400, "invalid_request", "printingEnabled is required")
				return
			}
			settings, err = users.Configure(r.Context(), owner, accounts.Settings{PrintingEnabled: *input.PrintingEnabled})
		}
		if err != nil {
			accountError(w, r, err)
			return
		}
		writeJSON(w, r, 200, settings)
	})
	if manager != nil {
		mux.HandleFunc("/api/v1/admin/dashboard", readOnly(func(w http.ResponseWriter, r *http.Request) {
			owner, ok := authorize(w, r, sessions)
			if !ok {
				return
			}
			dashboard, err := manager.Dashboard(r.Context(), owner)
			if err != nil {
				accountError(w, r, err)
				return
			}
			writeJSON(w, r, 200, dashboard)
		}))
		for _, action := range []string{"cancel", "confirm-cancel"} {
			mux.HandleFunc("/api/v1/admin/jobs/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
				if !method(w, r, "POST") {
					return
				}
				owner, ok := authorize(w, r, sessions)
				if !ok {
					return
				}
				var job printing.Job
				var err error
				if action == "confirm-cancel" {
					var input struct {
						DeviceStopped bool `json:"deviceStopped"`
					}
					if !decodeInput(w, r, &input) {
						return
					}
					job, err = manager.ConfirmCancel(r.Context(), owner, r.PathValue("id"), input.DeviceStopped)
				} else {
					job, err = manager.Cancel(r.Context(), owner, r.PathValue("id"))
				}
				if err != nil {
					if errors.Is(err, accounts.ErrForbidden) || errors.Is(err, accounts.ErrBanned) {
						accountError(w, r, err)
					} else {
						printError(w, r, err)
					}
					return
				}
				// Administrator controls return no document names or private print settings.
				writeJSON(w, r, 200, map[string]string{"id": job.ID, "state": job.State})
			})
		}
	}
}

func accountError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pairing.ErrLoginRequired):
		writeError(w, r, 401, "wechat_login_required", "WeChat login code is required")
	case errors.Is(err, accounts.ErrLogin):
		writeError(w, r, 401, "wechat_login_failed", "WeChat login code was rejected")
	case errors.Is(err, accounts.ErrUnavailable):
		writeError(w, r, 503, "wechat_unavailable", "WeChat login is unavailable")
	case errors.Is(err, accounts.ErrBanned):
		writeError(w, r, 403, "user_banned", "access has been disabled by the administrator")
	case errors.Is(err, accounts.ErrForbidden):
		writeError(w, r, 403, "admin_required", "administrator access is required")
	case errors.Is(err, accounts.ErrNotFound):
		writeError(w, r, 404, "user_not_found", "user not found")
	case errors.Is(err, accounts.ErrName):
		writeError(w, r, 400, "invalid_name", "name must be at most 40 characters without control characters")
	case errors.Is(err, accounts.ErrPaused):
		writeError(w, r, 409, "printing_paused", "new printing is paused by the administrator")
	default:
		writeError(w, r, 500, "internal_error", "account operation failed")
	}
}
