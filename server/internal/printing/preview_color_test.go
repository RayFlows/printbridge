package printing_test

import (
	"bytes"
	"context"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/pdf"
	"github.com/RayFlows/printbridge/server/internal/printers"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

type colorSource struct{ path string }

func (s colorSource) CopySource(_ context.Context, _, _ string, out io.Writer) (documents.Document, error) {
	file, err := os.Open(s.path)
	if err != nil {
		return documents.Document{}, err
	}
	defer file.Close()
	_, err = io.Copy(out, file)
	return documents.Document{Name: "synthetic.pdf", PageCount: 3}, err
}

func (colorSource) Discard(context.Context, string, string) error { return nil }

type colorBackend struct{ *backend }

func (b colorBackend) List(ctx context.Context) ([]printers.Printer, error) {
	list, err := b.backend.List(ctx)
	for i := range list {
		list[i].Capabilities.ColorModes = []string{"color", "monochrome", "auto-monochrome", "auto"}
	}
	return list, err
}

func TestPrintPreviewUsesColorModeIncludingPreviouslyCachedPlans(t *testing.T) {
	if os.Getenv("PRINTBRIDGE_TEST_POPPLER") != "1" {
		t.Skip("requires Poppler on PATH; only synthetic documents and a fake printing backend are used")
	}
	for _, mode := range []string{"monochrome", "auto-monochrome", "color", "auto", ""} {
		for _, cached := range []bool{false, true} {
			name := mode + "/fresh"
			if cached {
				name = mode + "/legacy-cache"
			}
			t.Run(name, func(t *testing.T) {
				f := setup(t)
				ctx := context.Background()
				processor := pdf.New("pdfinfo", "pdftoppm", time.Minute)
				source, err := filepath.Abs("../pdf/testdata/three-pages.pdf")
				if err != nil {
					t.Fatal(err)
				}
				f.service, err = printing.New(f.root, f.db, colorSource{source}, processor, colorBackend{f.backend})
				if err != nil {
					t.Fatal(err)
				}
				s := settings()
				s.ColorMode, s.Pages = mode, "1"
				plan, err := f.service.Prepare(ctx, "owner", "doc", s, "")
				if err != nil {
					t.Fatal(err)
				}
				dir := filepath.Join(f.root, "plans", plan.ID)
				if cached {
					// Reproduce a black-and-white plan carrying the old color PNG.
					if err := processor.Render(ctx, filepath.Join(dir, "layout.pdf"), 1, filepath.Join(dir, "page-1.png")); err != nil {
						t.Fatal(err)
					}
				}
				first, err := f.service.Preview(ctx, "owner", plan.ID, 1, "reading")
				if err != nil {
					t.Fatal(err)
				}
				second, err := f.service.Preview(ctx, "owner", plan.ID, 1, "reading")
				if err != nil || !bytes.Equal(first, second) {
					t.Fatal("repeat preview did not reuse the correct result", err)
				}
				image, err := png.Decode(bytes.NewReader(first))
				if err != nil {
					t.Fatal(err)
				}
				colored, dark := 0, 0
				for y := image.Bounds().Min.Y; y < image.Bounds().Max.Y; y++ {
					for x := image.Bounds().Min.X; x < image.Bounds().Max.X; x++ {
						r, g, b, _ := image.At(x, y).RGBA()
						if r != g || g != b {
							colored++
						}
						if r < 60000 && g < 60000 && b < 60000 {
							dark++
						}
					}
				}
				gray := mode == "monochrome" || mode == "auto-monochrome"
				if (gray && colored != 0) || (!gray && colored == 0) || dark == 0 {
					t.Fatalf("mode=%q: colored pixels=%d, nonblank pixels=%d", mode, colored, dark)
				}
				if f.backend.creates != 0 || f.backend.sends != 0 || f.backend.releases != 0 {
					t.Fatal("preparing a preview submitted printing")
				}
			})
		}
	}
}
