package pdf_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/pdf"
	"github.com/RayFlows/printbridge/server/internal/printing"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

func TestRealLayoutOrderPaddingAndCopies(t *testing.T) {
	if os.Getenv("PRINTBRIDGE_TEST_POPPLER") != "1" {
		t.Skip("set PRINTBRIDGE_TEST_POPPLER=1 with Poppler on PATH")
	}
	ctx := context.Background()
	p := pdf.New("pdfinfo", "pdftoppm", 30*time.Second)
	source, err := filepath.Abs("testdata/three-pages.pdf")
	if err != nil {
		t.Fatal(err)
	}
	s := printing.Settings{Media: "iso_a4_210x297mm", Orientation: "portrait", PagesPerSheet: 1, ScalePercent: 100, Sides: "manual-long-edge"}
	dir := t.TempDir()
	layout := filepath.Join(dir, "layout.pdf")
	count, err := p.Layout(ctx, source, layout, s, []int{3, 1, 2})
	if err != nil || count != 4 {
		t.Fatal(count, err)
	}
	readText := func(path string) []string {
		t.Helper()
		text, err := exec.Command("pdftotext", "-layout", path, "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSuffix(string(text), "\f"), "\f")
	}
	text := readText(layout)
	for i, want := range []string{"Page 3", "Page 1", "Page 2"} {
		if !strings.Contains(text[i], want) {
			t.Fatal("incorrect layout order", text)
		}
	}
	if len(text) != 4 || strings.TrimSpace(text[3]) != "" {
		t.Fatal("missing blank backside", text)
	}
	s.Copies, s.BackOrder = 2, "reverse"
	for _, phase := range []string{"front", "back"} {
		output := filepath.Join(dir, phase+".pdf")
		pages := printing.PhasePages(4, s, phase)
		rotation := 0
		if phase == "back" {
			rotation = 180
		}
		if err := p.Phase(ctx, layout, output, pages, rotation); err != nil {
			t.Fatal(err)
		}
		text = readText(output)
		wants := []string{"Page 3", "Page 2", "Page 3", "Page 2"}
		if phase == "back" {
			wants = []string{"", "Page 1", "", "Page 1"}
		}
		if len(text) != len(wants) {
			t.Fatal("copies not preserved", len(text), len(wants))
		}
		for i, want := range wants {
			if want == "" {
				if strings.TrimSpace(text[i]) != "" {
					t.Fatal("blank lost")
				}
			} else if !strings.Contains(text[i], want) {
				t.Fatal(text, wants)
			}
		}
	}
	for _, nup := range []int{2, 4, 6, 9, 16} {
		s.PagesPerSheet = nup
		s.Sides = "one-sided"
		s.Orientation = "landscape"
		s.ScalePercent = 90
		output := filepath.Join(dir, "nup-"+string(rune('a'+nup))+".pdf")
		count, err := p.Layout(ctx, source, output, s, []int{1, 2, 3})
		if err != nil || count != (3+nup-1)/nup {
			t.Fatal(nup, count, err)
		}
		actual, err := pdfapi.PageCountFile(ctx, output)
		if err != nil || actual != count {
			t.Fatal(actual, err)
		}
		if err := p.Render(ctx, output, 1, filepath.Join(dir, "nup.png")); err != nil {
			t.Fatal(err)
		}
	}
}
