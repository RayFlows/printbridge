package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RayFlows/printbridge/server/internal/cups"
)

type Config struct {
	ListenAddr           string
	LogLevel             slog.Level
	ShutdownTimeout      time.Duration
	CUPSURL              string
	CUPSTimeout          time.Duration
	PairingCode          string
	SessionTTL           time.Duration
	DataDir              string
	DocumentTTL          time.Duration
	PDFTimeout           time.Duration
	PDFInfo              string
	PDFToPPM             string
	OfficeCommand        string
	OfficeTimeout        time.Duration
	WatermarkFont        string
	ManualDuplexProfiles map[string]string
	WeChatAppID          string
	WeChatAppSecret      string
	AdminUserID          string
	CanonCancelHelper    string
}

func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		ListenAddr:      "127.0.0.1:8080",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 10 * time.Second,
		CUPSURL:         "http://localhost:631",
		CUPSTimeout:     5 * time.Second,
		SessionTTL:      24 * time.Hour,
		DataDir:         "data",
		DocumentTTL:     24 * time.Hour,
		PDFTimeout:      30 * time.Second,
		PDFInfo:         "pdfinfo",
		PDFToPPM:        "pdftoppm",
		OfficeCommand:   "soffice",
		OfficeTimeout:   2 * time.Minute,
	}

	if value := strings.TrimSpace(getenv("PRINTBRIDGE_LISTEN_ADDR")); value != "" {
		cfg.ListenAddr = value
	}
	_, port, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return Config{}, fmt.Errorf("PRINTBRIDGE_LISTEN_ADDR must be host:port: %w", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return Config{}, fmt.Errorf("PRINTBRIDGE_LISTEN_ADDR port must be between 0 and 65535")
	}

	switch value := strings.ToLower(strings.TrimSpace(getenv("PRINTBRIDGE_LOG_LEVEL"))); value {
	case "", "info":
		cfg.LogLevel = slog.LevelInfo
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("PRINTBRIDGE_LOG_LEVEL must be debug, info, warn, or error")
	}

	if value := strings.TrimSpace(getenv("PRINTBRIDGE_SHUTDOWN_TIMEOUT")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return Config{}, fmt.Errorf("PRINTBRIDGE_SHUTDOWN_TIMEOUT must be a positive duration")
		}
		cfg.ShutdownTimeout = duration
	}

	if value := strings.TrimSpace(getenv("PRINTBRIDGE_CUPS_URL")); value != "" {
		cfg.CUPSURL = value
	}
	if err := cups.ValidateEndpoint(cfg.CUPSURL); err != nil {
		return Config{}, fmt.Errorf("PRINTBRIDGE_CUPS_URL: %w", err)
	}
	if value := strings.TrimSpace(getenv("PRINTBRIDGE_CUPS_TIMEOUT")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 || duration > time.Minute {
			return Config{}, fmt.Errorf("PRINTBRIDGE_CUPS_TIMEOUT must be greater than 0 and at most 1m")
		}
		cfg.CUPSTimeout = duration
	}
	cfg.PairingCode = getenv("PRINTBRIDGE_PAIRING_CODE")
	if cfg.PairingCode != "" {
		if len(cfg.PairingCode) < 32 || len(cfg.PairingCode) > 128 ||
			strings.ContainsFunc(cfg.PairingCode, func(r rune) bool { return r < 33 || r > 126 }) {
			return Config{}, fmt.Errorf("PRINTBRIDGE_PAIRING_CODE must contain 32 to 128 printable ASCII characters without spaces")
		}
	}
	if value := strings.TrimSpace(getenv("PRINTBRIDGE_SESSION_TTL")); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 || duration > 30*24*time.Hour {
			return Config{}, fmt.Errorf("PRINTBRIDGE_SESSION_TTL must be greater than 0 and at most 720h")
		}
		cfg.SessionTTL = duration
	}

	for _, setting := range []struct {
		name   string
		target *string
	}{
		{"PRINTBRIDGE_DATA_DIR", &cfg.DataDir},
		{"PRINTBRIDGE_PDFINFO", &cfg.PDFInfo},
		{"PRINTBRIDGE_PDFTOPPM", &cfg.PDFToPPM},
		{"PRINTBRIDGE_OFFICE_COMMAND", &cfg.OfficeCommand},
		{"PRINTBRIDGE_WATERMARK_FONT", &cfg.WatermarkFont},
	} {
		if value := strings.TrimSpace(getenv(setting.name)); value != "" {
			*setting.target = value
		}
	}
	for _, setting := range []struct {
		name   string
		target *time.Duration
		max    time.Duration
	}{
		{"PRINTBRIDGE_DOCUMENT_TTL", &cfg.DocumentTTL, 7 * 24 * time.Hour},
		{"PRINTBRIDGE_PDF_TIMEOUT", &cfg.PDFTimeout, time.Minute},
		{"PRINTBRIDGE_OFFICE_TIMEOUT", &cfg.OfficeTimeout, 5 * time.Minute},
	} {
		if value := strings.TrimSpace(getenv(setting.name)); value != "" {
			duration, err := time.ParseDuration(value)
			if err != nil || duration <= 0 || duration > setting.max {
				return Config{}, fmt.Errorf("%s must be greater than 0 and at most %s", setting.name, setting.max)
			}
			*setting.target = duration
		}
	}
	if value := strings.TrimSpace(getenv("PRINTBRIDGE_MANUAL_DUPLEX_PROFILES")); value != "" {
		if err := json.Unmarshal([]byte(value), &cfg.ManualDuplexProfiles); err != nil || cfg.ManualDuplexProfiles == nil {
			return Config{}, fmt.Errorf("PRINTBRIDGE_MANUAL_DUPLEX_PROFILES must be a JSON object of queue names to profile IDs")
		}
	}
	cfg.WeChatAppID = strings.TrimSpace(getenv("PRINTBRIDGE_WECHAT_APP_ID"))
	cfg.CanonCancelHelper = strings.TrimSpace(getenv("PRINTBRIDGE_CANON_CANCEL_HELPER"))
	if cfg.CanonCancelHelper != "" {
		target, _ := url.Parse(cfg.CUPSURL)
		if !strings.HasPrefix(cfg.CanonCancelHelper, "/") ||
			(target.Port() != "" && target.Port() != "631") ||
			!slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, target.Hostname()) {
			return Config{}, fmt.Errorf("PRINTBRIDGE_CANON_CANCEL_HELPER requires an absolute Linux path and local CUPS")
		}
	}
	cfg.WeChatAppSecret = strings.TrimSpace(getenv("PRINTBRIDGE_WECHAT_APP_SECRET"))
	cfg.AdminUserID = strings.TrimSpace(getenv("PRINTBRIDGE_ADMIN_USER_ID"))
	if (cfg.WeChatAppID == "") != (cfg.WeChatAppSecret == "") {
		return Config{}, fmt.Errorf("PRINTBRIDGE_WECHAT_APP_ID and PRINTBRIDGE_WECHAT_APP_SECRET must be set together")
	}
	if cfg.AdminUserID != "" {
		if cfg.WeChatAppID == "" || len(cfg.AdminUserID) != 32 || strings.ContainsFunc(cfg.AdminUserID, func(r rune) bool {
			return !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9')
		}) {
			return Config{}, fmt.Errorf("PRINTBRIDGE_ADMIN_USER_ID requires WeChat login and a 32-character lowercase hexadecimal user ID")
		}
	}
	return cfg, nil
}
