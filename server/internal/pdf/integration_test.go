package pdf_test

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/pdf"
)

func TestPopplerWithSyntheticDocument(t *testing.T) {
	if os.Getenv("PRINTBRIDGE_TEST_POPPLER") != "1" {
		t.Skip("opt in with PRINTBRIDGE_TEST_POPPLER=1 and Poppler on PATH")
	}
	for _, name := range []string{"pdfinfo", "pdftoppm"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatal(err)
		}
	}
	source, err := filepath.Abs("testdata/three-pages.pdf")
	if err != nil {
		t.Fatal(err)
	}
	processor := pdf.New("pdfinfo", "pdftoppm", 30*time.Second)
	pages, err := processor.Inspect(context.Background(), source)
	if err != nil || pages != 3 {
		t.Fatalf("pages=%d error=%v", pages, err)
	}
	var previous []byte
	for page := 1; page <= pages; page++ {
		output := filepath.Join(t.TempDir(), "page.png")
		if err := processor.Render(context.Background(), source, page, output); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		bounds := img.Bounds()
		if bounds.Dx() > 1600 || bounds.Dy() > 1600 {
			t.Fatal("preview dimensions exceeded limit")
		}
		if (page == 2) != (bounds.Dx() > bounds.Dy()) {
			t.Fatal("page orientation was not preserved")
		}
		if bytes.Equal(previous, data) {
			t.Fatal("wrong page was reused")
		}
		previous = data
	}
}
