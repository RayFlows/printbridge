package pdf_test

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/pdf"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func TestPhoneJPEGOrientation(t *testing.T) {
	if os.Getenv("PRINTBRIDGE_TEST_POPPLER") != "1" {
		t.Skip("requires Poppler on PATH")
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "photo.pdf")
	preview := filepath.Join(dir, "preview.png")
	p := pdf.New("pdfinfo", "pdftoppm", 30*time.Second)
	if err := p.Prepare(context.Background(), "testdata/orientation.jpg", "phone.jpg", output); err != nil {
		t.Fatal(err)
	}
	if err := p.Render(context.Background(), output, 1, preview); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(preview)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	minX, minY, maxX, maxY := 1600, 1600, 0, 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r < 40*257 && g > 90*257 && g < 160*257 && b > 70*257 && b < 160*257 {
				minX = min(minX, x)
				minY = min(minY, y)
				maxX = max(maxX, x)
				maxY = max(maxY, y)
			}
		}
	}
	if maxY-minY < 4*(maxX-minX) || maxY-minY < 100 {
		t.Fatal("EXIF 6 did not rotate the horizontal teal bar clockwise")
	}
}

func TestImagesArePreparedAsRealPDFs(t *testing.T) {
	t.Parallel()
	p := pdf.New("unused", "unused", time.Second)
	for _, ext := range []string{"jpg", "png", "webp", "bmp", "tiff", "gif"} {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()
			output := filepath.Join(t.TempDir(), "source.pdf")
			if err := p.Prepare(context.Background(), filepath.Join("testdata", "sample."+ext), "sample."+ext, output); err != nil {
				t.Fatal(err)
			}
			count, err := pdfapi.PageCountFile(context.Background(), output)
			want := 1
			if ext == "tiff" {
				want = 2
			}
			if err != nil || count != want {
				t.Fatal(count, err)
			}
		})
	}
}
func TestImageAndOfficeFailures(t *testing.T) {
	t.Parallel()
	p := pdf.New("unused", "unused", time.Second).WithOffice(filepath.Join(t.TempDir(), "missing-converter"), time.Second)
	bad := filepath.Join(t.TempDir(), "corrupt")
	os.WriteFile(bad, []byte("invalid"), 0600)
	for _, tt := range []struct {
		name string
		want error
	}{{"image.jpg", documents.ErrConversion}, {"macro.exe", documents.ErrUnsupported}, {"office.docx", documents.ErrConverter}} {
		err := p.Prepare(context.Background(), bad, tt.name, filepath.Join(t.TempDir(), "out.pdf"))
		if !errors.Is(err, tt.want) {
			t.Fatalf("%s: %v", tt.name, err)
		}
	}
}
func TestOfficeWithRealConverter(t *testing.T) {
	command := os.Getenv("PRINTBRIDGE_TEST_OFFICE")
	if command == "" {
		t.Skip("set PRINTBRIDGE_TEST_OFFICE to a local soffice command")
	}
	p := pdf.New("pdfinfo", "pdftoppm", 30*time.Second).WithOffice(command, 90*time.Second)
	for _, tt := range []struct {
		ext   string
		pages int
	}{
		{"docx", 2}, {"xlsx", 1}, {"pptx", 2}, {"doc", 2}, {"xls", 1}, {"ppt", 2}, {"odt", 2}, {"ods", 1}, {"odp", 2}, {"rtf", 1}, {"txt", 1}, {"csv", 1},
	} {
		t.Run(tt.ext, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "source.pdf")
			input, err := filepath.Abs(filepath.Join("testdata", "sample."+tt.ext))
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Prepare(context.Background(), input, "sample."+tt.ext, output); err != nil {
				t.Fatal(err)
			}
			count, err := p.Inspect(context.Background(), output)
			if err != nil || count != tt.pages {
				t.Fatalf("pages=%d want=%d error=%v", count, tt.pages, err)
			}
			if err := p.Render(context.Background(), output, 1, filepath.Join(filepath.Dir(output), "preview.png")); err != nil {
				t.Fatal(err)
			}
			// Check the output parses independently of the converter and Poppler.
			if err := pdfapi.ValidateFile(context.Background(), output, model.NewStatelessConfiguration(), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
