package pdf_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/pdf"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

func colorFixture(t *testing.T, dir string) string {
	t.Helper()
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 4 /Kids [3 0 R 5 0 R 7 0 R 9 0 R] >>"}
	for i, color := range []string{"1 0 0", "0 1 0", "0 0 1", "1 1 0"} {
		content := fmt.Sprintf("%s rg 60 90 80 120 re f\n", color)
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 300] /Resources << >> /Contents %d 0 R >>", 4+2*i),
			fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{}
	for i, object := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	start := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, start)
	path := filepath.Join(dir, "colors.pdf")
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func renderLayout(t *testing.T, source, dir string, s printing.Settings, pages []int) image.Image {
	t.Helper()
	p := pdf.New("pdfinfo", "pdftoppm", 30*time.Second)
	output := filepath.Join(dir, "layout.pdf")
	if _, err := p.Layout(context.Background(), source, output, s, pages); err != nil {
		t.Fatal(err)
	}
	picture := filepath.Join(dir, "page.png")
	if err := p.Render(context.Background(), output, 1, picture); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(picture)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func requireRendering(t *testing.T) {
	t.Helper()
	if os.Getenv("PRINTBRIDGE_TEST_POPPLER") != "1" {
		t.Skip("set PRINTBRIDGE_TEST_POPPLER=1 with Poppler on PATH")
	}
}

func TestRealScalingPreservesActualSizeAndClipsEachCell(t *testing.T) {
	requireRendering(t)
	source := colorFixture(t, t.TempDir())
	for _, mode := range []string{"fit", "shrink", "actual", "fill", "custom"} {
		t.Run(mode, func(t *testing.T) {
			s := printing.Settings{Media: "a4", Orientation: "portrait", PagesPerSheet: 1,
				ScalePercent: 50, Sides: "one-sided", Scaling: mode}
			img := renderLayout(t, source, t.TempDir(), s, []int{1})
			bounds := img.Bounds()
			first, last := bounds.Max.X, 0
			for x := 0; x < bounds.Max.X; x++ {
				r, g, b, _ := img.At(x, bounds.Max.Y/2).RGBA()
				if r > 60000 && g < 3000 && b < 3000 {
					first = min(first, x)
					last = max(last, x)
				}
			}
			w, h, _ := printing.PaperDimensions("a4", "portrait")
			fit := math.Min((w-10*72/25.4)/200, (h-10*72/25.4)/300)
			scale := map[string]float64{"fit": fit, "shrink": 1, "actual": 1, "fill": math.Max(w/200, h/300), "custom": .5}[mode]
			want := 80 * scale * float64(bounds.Dx()) / w
			if math.Abs(float64(last-first+1)-want) > 3 {
				t.Fatalf("%s: rendered width %d, want %.1f", mode, last-first+1, want)
			}
		})
	}
	// At 400%, the red first page covers its cell, but must not cover the green neighbour.
	s := printing.Settings{Media: "a4", Orientation: "portrait", PagesPerSheet: 4,
		ScalePercent: 400, Sides: "one-sided", Scaling: "custom"}
	img := renderLayout(t, source, t.TempDir(), s, []int{1, 2, 3, 4})
	r, g, b, _ := img.At(img.Bounds().Dx()*3/4, img.Bounds().Dy()/4).RGBA()
	if r > 1000 || g < 64000 || b > 1000 {
		t.Fatal("enlarged content escaped its cell")
	}
}

func TestRealNUpTraversalAndBorders(t *testing.T) {
	requireRendering(t)
	source := colorFixture(t, t.TempDir())
	for order, quadrants := range map[string][]string{
		"lrtb": {"red", "green", "blue", "yellow"}, "rltb": {"green", "red", "yellow", "blue"},
		"tblr": {"red", "blue", "green", "yellow"}, "tbrl": {"blue", "red", "yellow", "green"},
	} {
		t.Run(order, func(t *testing.T) {
			s := printing.Settings{Media: "a4", Orientation: "portrait", PagesPerSheet: 4,
				ScalePercent: 100, Sides: "one-sided", Scaling: "fit", LayoutOrder: order, PageBorder: true}
			img := renderLayout(t, source, t.TempDir(), s, []int{1, 2, 3, 4})
			colors := map[string][3]uint32{"red": {65535, 0, 0}, "green": {0, 65535, 0}, "blue": {0, 0, 65535}, "yellow": {65535, 65535, 0}}
			for i, color := range quadrants {
				x, y := img.Bounds().Dx()*(1+2*(i%2))/4, img.Bounds().Dy()*(1+2*(i/2))/4
				r, g, b, _ := img.At(x, y).RGBA()
				if [3]uint32{r, g, b} != colors[color] {
					t.Fatalf("quadrant %d: got %d/%d/%d want %s", i, r, g, b, color)
				}
			}
			dark := false
			for x := img.Bounds().Dx()/2 - 2; x <= img.Bounds().Dx()/2+2; x++ {
				r, g, b, _ := img.At(x, img.Bounds().Dy()/4).RGBA()
				if r < 60000 && g < 60000 && b < 60000 {
					dark = true
				}
			}
			if !dark {
				t.Fatal("cell border was not rendered")
			}
		})
	}
}
