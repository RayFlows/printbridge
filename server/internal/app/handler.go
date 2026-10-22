package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/RayFlows/printbridge/server/internal/accounts"
	"github.com/RayFlows/printbridge/server/internal/api"
	"github.com/RayFlows/printbridge/server/internal/config"
	"github.com/RayFlows/printbridge/server/internal/cups"
	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/management"
	"github.com/RayFlows/printbridge/server/internal/pairing"
	"github.com/RayFlows/printbridge/server/internal/pdf"
	"github.com/RayFlows/printbridge/server/internal/printing"
	"github.com/RayFlows/printbridge/server/internal/store"
	"github.com/RayFlows/printbridge/server/internal/wechat"
)

type Handler struct {
	http.Handler
	store    *store.SQLite
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
	closeErr error
}

func NewHandler(cfg config.Config, version string) (*Handler, error) {
	var login accounts.LoginVerifier
	if cfg.WeChatAppID != "" {
		login = wechat.NewLogin(cfg.WeChatAppID, cfg.WeChatAppSecret, nil)
	}
	return newHandler(cfg, version, login)
}

func newHandler(cfg config.Config, version string, login accounts.LoginVerifier) (*Handler, error) {
	reader, err := cups.New(cfg.CUPSURL, cfg.CUPSTimeout)
	if err != nil {
		return nil, err
	}
	backend, err := printing.WithManualDuplexProfiles(reader, cfg.ManualDuplexProfiles)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return nil, err
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "printbridge.db"))
	if err != nil {
		return nil, err
	}
	processor := pdf.New(cfg.PDFInfo, cfg.PDFToPPM, cfg.PDFTimeout).
		WithOffice(cfg.OfficeCommand, cfg.OfficeTimeout).WithWatermarkFont(cfg.WatermarkFont)
	docs, err := documents.New(filepath.Join(cfg.DataDir, "documents"), db, processor, cfg.DocumentTTL)
	if err == nil {
		err = docs.Cleanup(context.Background())
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	prints, err := printing.New(filepath.Join(cfg.DataDir, "printing"), db, docs, processor, backend)
	if err == nil && cfg.CanonCancelHelper != "" {
		prints.WithDeviceCanceler(cups.DeviceCanceler{Helper: cfg.CanonCancelHelper})
	}
	if err == nil {
		err = prints.Cleanup(context.Background())
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	sessions, err := pairing.NewPersistent(cfg.PairingCode, cfg.SessionTTL, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	var manager *management.Service
	if cfg.WeChatAppID != "" && cfg.WeChatAppSecret != "" {
		users := accounts.New(db, login, cfg.WeChatAppID, cfg.AdminUserID)
		sessions.WithUsers(users)
		prints.WithAdmission(users.AllowPrinting)
		manager = management.New(users, db, prints, prints)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Handler{Handler: api.NewManagedHandler(version, sessions, prints, docs, prints, manager),
		store: db, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		workerDone := make(chan struct{})
		go func() { defer close(workerDone); prints.Run(ctx) }()
		defer func() { <-workerDone }()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := docs.Cleanup(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("document cleanup failed")
				}
				if err := prints.Cleanup(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("print cleanup failed")
				}
			}
		}
	}()
	return h, nil
}

func (h *Handler) Close() error {
	h.once.Do(func() {
		h.cancel()
		<-h.done
		h.closeErr = h.store.Close()
	})
	return h.closeErr
}
