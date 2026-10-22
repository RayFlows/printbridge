package config_test

import (
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/config"
)

func TestDefaults(t *testing.T) {
	t.Parallel()
	got, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		ListenAddr:      "127.0.0.1:8080",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 10 * time.Second,
		CUPSURL:         "http://localhost:631",
		CUPSTimeout:     5 * time.Second,
		SessionTTL:      24 * time.Hour,
		DataDir:         "data", DocumentTTL: 24 * time.Hour, PDFTimeout: 30 * time.Second,
		PDFInfo: "pdfinfo", PDFToPPM: "pdftoppm",
		OfficeCommand: "soffice", OfficeTimeout: 2 * time.Minute,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

func TestDeviceCancellationRequiresLocalCUPSAndAnAbsoluteHelper(t *testing.T) {
	for _, tc := range []struct {
		endpoint, helper string
		valid            bool
	}{
		{"http://localhost:631", "/usr/local/libexec/printbridge-canon-cancel", true},
		{"http://127.0.0.1:631", "/opt/printbridge/helper", true},
		{"http://127.0.0.1:8631", "/opt/printbridge/helper", false},
		{"http://[::1]:631", "/opt/printbridge/helper", true},
		{"http://cups.example.test:631", "/opt/printbridge/helper", false},
		{"http://localhost:631", "./helper", false},
	} {
		cfg, err := config.Load(func(key string) string {
			switch key {
			case "PRINTBRIDGE_CUPS_URL":
				return tc.endpoint
			case "PRINTBRIDGE_CANON_CANCEL_HELPER":
				return tc.helper
			}
			return ""
		})
		if (err == nil) != tc.valid || (tc.valid && cfg.CanonCancelHelper != tc.helper) {
			t.Fatal(tc, err)
		}
	}
}

func TestConfigurationOverrides(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"PRINTBRIDGE_LISTEN_ADDR":            " [::1]:9090 ",
		"PRINTBRIDGE_LOG_LEVEL":              " DEBUG ",
		"PRINTBRIDGE_SHUTDOWN_TIMEOUT":       " 750ms ",
		"PRINTBRIDGE_CUPS_URL":               " https://cups.example.test:631/ ",
		"PRINTBRIDGE_CUPS_TIMEOUT":           " 8s ",
		"PRINTBRIDGE_PAIRING_CODE":           strings.Repeat("x", 32),
		"PRINTBRIDGE_SESSION_TTL":            " 2h ",
		"PRINTBRIDGE_DATA_DIR":               " local-data ",
		"PRINTBRIDGE_DOCUMENT_TTL":           " 1h ",
		"PRINTBRIDGE_PDF_TIMEOUT":            " 10s ",
		"PRINTBRIDGE_PDFINFO":                " pdfinfo-custom ",
		"PRINTBRIDGE_PDFTOPPM":               " pdftoppm-custom ",
		"PRINTBRIDGE_OFFICE_COMMAND":         " soffice-custom ",
		"PRINTBRIDGE_OFFICE_TIMEOUT":         " 3m ",
		"PRINTBRIDGE_MANUAL_DUPLEX_PROFILES": `{"VirtualPrinter":"rear-face-up-v1"}`,
	}
	got, err := config.Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		ListenAddr:      "[::1]:9090",
		LogLevel:        slog.LevelDebug,
		ShutdownTimeout: 750 * time.Millisecond,
		CUPSURL:         "https://cups.example.test:631/",
		CUPSTimeout:     8 * time.Second,
		PairingCode:     strings.Repeat("x", 32),
		SessionTTL:      2 * time.Hour,
		DataDir:         "local-data", DocumentTTL: time.Hour, PDFTimeout: 10 * time.Second,
		PDFInfo: "pdfinfo-custom", PDFToPPM: "pdftoppm-custom",
		OfficeCommand: "soffice-custom", OfficeTimeout: 3 * time.Minute,
		ManualDuplexProfiles: map[string]string{"VirtualPrinter": "rear-face-up-v1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

func TestInvalidConfigurationNamesTheSetting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"missing host and separator", "PRINTBRIDGE_LISTEN_ADDR", "8080"},
		{"URL instead of address", "PRINTBRIDGE_LISTEN_ADDR", "http://localhost:8080"},
		{"unbracketed IPv6", "PRINTBRIDGE_LISTEN_ADDR", "::1:8080"},
		{"missing port", "PRINTBRIDGE_LISTEN_ADDR", "localhost:"},
		{"named port", "PRINTBRIDGE_LISTEN_ADDR", "localhost:http"},
		{"negative port", "PRINTBRIDGE_LISTEN_ADDR", "localhost:-1"},
		{"port out of range", "PRINTBRIDGE_LISTEN_ADDR", "localhost:65536"},
		{"unknown log level", "PRINTBRIDGE_LOG_LEVEL", "trace"},
		{"zero shutdown timeout", "PRINTBRIDGE_SHUTDOWN_TIMEOUT", "0s"},
		{"negative shutdown timeout", "PRINTBRIDGE_SHUTDOWN_TIMEOUT", "-1s"},
		{"invalid shutdown timeout", "PRINTBRIDGE_SHUTDOWN_TIMEOUT", "forever"},
		{"IPP scheme is not HTTP", "PRINTBRIDGE_CUPS_URL", "ipp://localhost:631"},
		{"credentials in URL", "PRINTBRIDGE_CUPS_URL", "http://secret:secret@localhost:631"},
		{"printer path", "PRINTBRIDGE_CUPS_URL", "http://localhost:631/printers/test"},
		{"URL query", "PRINTBRIDGE_CUPS_URL", "http://localhost:631?secret=secret"},
		{"URL fragment", "PRINTBRIDGE_CUPS_URL", "http://localhost:631#fragment"},
		{"invalid port", "PRINTBRIDGE_CUPS_URL", "http://localhost:bad"},
		{"out of range CUPS port", "PRINTBRIDGE_CUPS_URL", "http://localhost:65536"},
		{"empty CUPS port", "PRINTBRIDGE_CUPS_URL", "http://localhost:"},
		{"zero CUPS timeout", "PRINTBRIDGE_CUPS_TIMEOUT", "0s"},
		{"excessive CUPS timeout", "PRINTBRIDGE_CUPS_TIMEOUT", "61s"},
		{"short pairing code", "PRINTBRIDGE_PAIRING_CODE", "123456"},
		{"whitespace pairing code", "PRINTBRIDGE_PAIRING_CODE", strings.Repeat(" ", 32)},
		{"long pairing code", "PRINTBRIDGE_PAIRING_CODE", strings.Repeat("x", 129)},
		{"zero session TTL", "PRINTBRIDGE_SESSION_TTL", "0s"},
		{"invalid session TTL", "PRINTBRIDGE_SESSION_TTL", "forever"},
		{"excessive session TTL", "PRINTBRIDGE_SESSION_TTL", "721h"},
		{"invalid document TTL", "PRINTBRIDGE_DOCUMENT_TTL", "0s"},
		{"excessive document TTL", "PRINTBRIDGE_DOCUMENT_TTL", "169h"},
		{"excessive PDF timeout", "PRINTBRIDGE_PDF_TIMEOUT", "61s"},
		{"excessive Office timeout", "PRINTBRIDGE_OFFICE_TIMEOUT", "6m"},
		{"invalid duplex profile object", "PRINTBRIDGE_MANUAL_DUPLEX_PROFILES", `[]`},
		{"null duplex profiles", "PRINTBRIDGE_MANUAL_DUPLEX_PROFILES", `null`},
		{"non-string duplex profile", "PRINTBRIDGE_MANUAL_DUPLEX_PROFILES", `{"virtual":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Load(func(key string) string {
				if key == tt.key {
					return tt.value
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("error = %v, want an error naming %s", err, tt.key)
			}
		})
	}
}

func TestEphemeralPortIsAllowed(t *testing.T) {
	t.Parallel()
	got, err := config.Load(func(key string) string {
		if key == "PRINTBRIDGE_LISTEN_ADDR" {
			return "127.0.0.1:0"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != "127.0.0.1:0" {
		t.Fatalf("listen address = %q", got.ListenAddr)
	}
}

func TestWeChatAndAdministratorConfiguration(t *testing.T) {
	for _, values := range []map[string]string{
		{"PRINTBRIDGE_WECHAT_APP_ID": "mock-app"},
		{"PRINTBRIDGE_WECHAT_APP_SECRET": "test-secret"},
		{"PRINTBRIDGE_ADMIN_USER_ID": strings.Repeat("a", 32)},
		{"PRINTBRIDGE_WECHAT_APP_ID": "mock-app", "PRINTBRIDGE_WECHAT_APP_SECRET": "test-secret", "PRINTBRIDGE_ADMIN_USER_ID": "not-a-user-id"},
	} {
		if _, err := config.Load(func(key string) string { return values[key] }); err == nil || strings.Contains(err.Error(), "test-secret") {
			t.Fatal("invalid identity configuration or secret disclosure", err)
		}
	}
	values := map[string]string{"PRINTBRIDGE_WECHAT_APP_ID": "mock-app", "PRINTBRIDGE_WECHAT_APP_SECRET": "test-secret",
		"PRINTBRIDGE_ADMIN_USER_ID": strings.Repeat("a", 32)}
	cfg, err := config.Load(func(key string) string { return values[key] })
	if err != nil || cfg.AdminUserID != values["PRINTBRIDGE_ADMIN_USER_ID"] {
		t.Fatal(err)
	}
}
