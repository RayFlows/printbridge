package pdf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
)

func TestMain(m *testing.M) {
	// The test executable acts as a bounded fake external PDF utility.
	if mode := os.Getenv("PRINTBRIDGE_TEST_PDF_MODE"); mode != "" && len(os.Args) > 1 && os.Args[1] == "-enc" {
		switch mode {
		case "valid":
			fmt.Print("Title: Synthetic\nPages:          16\nEncrypted:      no\n")
		case "encrypted":
			fmt.Print("Pages: 16\nEncrypted: yes (print:yes)\n")
		case "malformed":
			fmt.Print("Pages: unknown\n")
		case "too-many":
			fmt.Print("Pages: 501\n")
		case "failure":
			os.Exit(1)
		case "timeout":
			time.Sleep(time.Second * 10)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInspectionExternalToolBoundaries(t *testing.T) {
	source := filepath.Join(t.TempDir(), "input with spaces.pdf")
	if err := os.WriteFile(source, []byte("%PDF-synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode string
		want error
	}{
		{"valid", nil}, {"encrypted", documents.ErrInvalidPDF}, {"malformed", documents.ErrInvalidPDF},
		{"too-many", documents.ErrTooManyPages}, {"failure", documents.ErrInvalidPDF},
		{"timeout", documents.ErrTimeout},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Setenv("PRINTBRIDGE_TEST_PDF_MODE", tc.mode)
			timeout := 5 * time.Second
			if tc.mode == "timeout" {
				timeout = 50 * time.Millisecond
			}
			pages, err := New(os.Args[0], os.Args[0], timeout).Inspect(context.Background(), source)
			if !errors.Is(err, tc.want) || (err == nil && pages != 16) {
				t.Fatalf("pages=%d error=%v", pages, err)
			}
		})
	}
	_, err := New(filepath.Join(t.TempDir(), "missing-tool"), "", time.Second).Inspect(context.Background(), source)
	if !errors.Is(err, documents.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("not PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = New("must-not-run", "", time.Second).Inspect(context.Background(), source)
	if !errors.Is(err, documents.ErrInvalidPDF) {
		t.Fatal(err)
	}
}

func TestDiagnosticCaptureIsBounded(t *testing.T) {
	t.Parallel()
	buffer := &limitedBuffer{remaining: 16}
	data := []byte(strings.Repeat("x", 100))
	for i := 0; i < 10; i++ {
		if n, err := buffer.Write(data); n != 100 || err != nil {
			t.Fatal(n, err)
		}
	}
	if buffer.Len() != 16 {
		t.Fatal("unbounded process output")
	}
}
