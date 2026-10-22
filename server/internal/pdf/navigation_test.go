package pdf_test

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/pdf"
	"github.com/RayFlows/printbridge/server/internal/printing"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func navigationFixture(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	input, err := os.Open(colorFixture(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	config := model.NewStatelessConfiguration()
	config.ValidationMode = model.ValidationRelaxed
	doc, err := pdfapi.ReadValidateAndOptimize(ctx, input, config, nil)
	input.Close()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := doc.PageDictIndRef(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := doc.IndRefForNewObject(types.Array{*ref, types.Name("Fit")})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := doc.IndRefForNewObject(types.Dict{
		"Names":  types.Array{types.StringLiteral("page.1"), *dest},
		"Limits": types.Array{types.StringLiteral("page.1"), types.StringLiteral("page.1")},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc.RootDict["Names"] = types.Dict{"Dests": types.Dict{"Kids": types.Array{*leaf}}}
	path := filepath.Join(dir, "navigation.pdf")
	if err := pdfapi.WriteContextFile(ctx, doc, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLayoutWithNamedDestinationsCanBeReorderedAndSplit(t *testing.T) {
	requireRendering(t)
	p := pdf.New("pdfinfo", "pdftoppm", time.Minute)
	source := navigationFixture(t)
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, scaling := range []string{"", "fit"} {
		t.Run("scaling="+scaling, func(t *testing.T) {
			dir := t.TempDir()
			layout := filepath.Join(dir, "layout.pdf")
			s := printing.Settings{Media: "a4", Orientation: "portrait", PagesPerSheet: 1,
				ScalePercent: 100, Sides: "manual-long-edge", Scaling: scaling}
			if _, err := p.Layout(ctx, source, layout, s, []int{1, 2, 3}); err != nil {
				t.Fatal("layout", err)
			}
			reversed := filepath.Join(dir, "reversed.pdf")
			if err := p.Phase(ctx, layout, reversed, []int{3, 4, 1, 2}, 0); err != nil {
				t.Fatal("reverse", err)
			}
			phase := filepath.Join(dir, "phase.pdf")
			if err := p.Phase(ctx, reversed, phase, []int{1, 3, 1, 3}, 180); err != nil {
				t.Fatal("split/copies/rotation", err)
			}
			for i, want := range [][3]uint32{{0, 0, 65535}, {65535, 0, 0}, {0, 0, 65535}, {65535, 0, 0}} {
				path := filepath.Join(dir, "preview.png")
				if err := p.Render(ctx, phase, i+1, path); err != nil {
					t.Fatal("render", err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				img, err := png.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				r, g, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
				if [3]uint32{r, g, b} != want {
					t.Fatal("page content/order changed", i+1, r, g, b)
				}
			}
		})
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("uploaded original was modified", err)
	}
}
