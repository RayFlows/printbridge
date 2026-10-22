package pdf_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RayFlows/printbridge/server/internal/pdf"
	"github.com/RayFlows/printbridge/server/internal/printing"
	"golang.org/x/image/font/gofont/goregular"
)

func effectImage(t *testing.T, palette bool) string {
	t.Helper()
	dir := t.TempDir()
	var img draw.Image = image.NewRGBA(image.Rect(0, 0, 300, 200))
	if palette {
		img = image.NewPaletted(img.Bounds(), color.Palette{color.White, color.Black, color.RGBA{255, 0, 0, 255}})
	}
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(10, 10, 90, 90), image.NewUniform(color.RGBA{255, 0, 0, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(200, 110, 280, 190), image.NewUniform(color.Black), image.Point{}, draw.Src)
	path := filepath.Join(dir, "source.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, img)
	if err = errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "image.pdf")
	if err := pdf.New("pdfinfo", "pdftoppm", time.Minute).Prepare(context.Background(), path, "source.png", out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRealImageEffectsReflectAndInvertWithoutPaintingMargins(t *testing.T) {
	requireRendering(t)
	for _, palette := range []bool{false, true} {
		t.Run(map[bool]string{false: "rgb", true: "indexed"}[palette], func(t *testing.T) {
			source := effectImage(t, palette)
			s := printing.Settings{Media: "a4", Orientation: "portrait", PagesPerSheet: 1,
				ScalePercent: 100, Scaling: "fit", Sides: "one-sided"}
			plain := renderLayout(t, source, t.TempDir(), s, []int{1})
			s.Mirror = true
			mirror := renderLayout(t, source, t.TempDir(), s, []int{1})
			s.Mirror, s.InvertImages = false, true
			inverse := renderLayout(t, source, t.TempDir(), s, []int{1})
			mirrored, inverted := 0, 0
			w, h := plain.Bounds().Dx(), plain.Bounds().Dy()
			for y := 0; y < h; y += 4 {
				for x := 0; x < w; x += 4 {
					r, g, b, _ := plain.At(x, y).RGBA()
					if r > 64000 && g < 1000 && b < 1000 {
						mr, mg, mb, _ := mirror.At(w-1-x, y).RGBA()
						if mr > 64000 && mg < 1000 && mb < 1000 {
							mirrored++
						}
						ir, ig, ib, _ := inverse.At(x, y).RGBA()
						if ir < 1000 && ig > 64000 && ib > 64000 {
							inverted++
						}
					}
				}
			}
			if mirrored < 100 || inverted < 100 {
				t.Fatal("missing reflected red or inverted cyan region", mirrored, inverted)
			}
			r, g, b, _ := inverse.At(4, 4).RGBA()
			if r < 65000 || g < 65000 || b < 65000 {
				t.Fatal("inversion painted the paper margin")
			}
		})
	}
}

func TestRealWatermarkAndMirrorPreserveDuplexBlank(t *testing.T) {
	requireRendering(t)
	source, _ := filepath.Abs("testdata/three-pages.pdf")
	for _, text := range []string{"TEST, 100% : (draft) / \\", "A", "|", "   "} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			p := pdf.New("pdfinfo", "pdftoppm", time.Minute)
			s := printing.Settings{Media: "a4", Orientation: "landscape", PagesPerSheet: 2,
				ScalePercent: 100, Scaling: "fit", Sides: "manual-long-edge", Watermark: text, Mirror: true}
			output := filepath.Join(dir, "layout.pdf")
			n, err := p.Layout(context.Background(), source, output, s, []int{1})
			if err != nil || n != 2 {
				t.Fatal(n, err)
			}
			picture := filepath.Join(dir, "blank.png")
			if err := p.Render(context.Background(), output, 2, picture); err != nil {
				t.Fatal(err)
			}
			file, _ := os.Open(picture)
			img, err := png.Decode(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			for y := 0; y < img.Bounds().Dy(); y += 8 {
				for x := 0; x < img.Bounds().Dx(); x += 8 {
					r, g, b, _ := img.At(x, y).RGBA()
					if r < 65000 || g < 65000 || b < 65000 {
						t.Fatal("padding face contains a watermark or mirrored content")
					}
				}
			}
		})
	}
}

func TestRealChineseWatermark(t *testing.T) {
	requireRendering(t)
	font := os.Getenv("PRINTBRIDGE_TEST_WATERMARK_FONT")
	if font == "" {
		t.Skip("set PRINTBRIDGE_TEST_WATERMARK_FONT to a CJK TTF/OTF/TTC")
	}
	dir := t.TempDir()
	p := pdf.New("pdfinfo", "pdftoppm", time.Minute).WithWatermarkFont(font)
	source, _ := filepath.Abs("testdata/three-pages.pdf")
	s := printing.Settings{Media: "a4", Orientation: "portrait", PagesPerSheet: 1,
		ScalePercent: 100, Scaling: "fit", Sides: "one-sided", Watermark: "内部资料 PrintBridge"}
	output := filepath.Join(dir, "layout.pdf")
	if _, err := p.Layout(context.Background(), source, output, s, []int{1}); err != nil {
		t.Fatal(err)
	}
	if err := p.Render(context.Background(), output, 1, filepath.Join(dir, "chinese.png")); err != nil {
		t.Fatal(err)
	}
}

func TestWatermarkMissingFontNeverProducesSuccessfulOutput(t *testing.T) {
	dir := t.TempDir()
	p := pdf.New("pdfinfo", "pdftoppm", time.Minute).WithWatermarkFont(filepath.Join(dir, "missing.ttf"))
	source, _ := filepath.Abs("testdata/three-pages.pdf")
	s := printing.Settings{Media: "a4", Orientation: "portrait", PagesPerSheet: 1,
		ScalePercent: 100, Scaling: "fit", Sides: "one-sided", Watermark: "draft"}
	output := filepath.Join(dir, "layout.pdf")
	if _, err := p.Layout(context.Background(), source, output, s, []int{1}); !errors.Is(err, printing.ErrWatermarkFont) {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed watermark left an output")
	}
}

func TestWatermarkUnsupportedGlyphFailsInsteadOfDrawingTofu(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latin.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0600); err != nil {
		t.Fatal(err)
	}
	p := pdf.New("unused", "unused", time.Minute).WithWatermarkFont(path)
	source, _ := filepath.Abs("testdata/three-pages.pdf")
	s := printing.Settings{Media: "a4", Orientation: "portrait", PagesPerSheet: 1,
		ScalePercent: 100, Scaling: "fit", Sides: "one-sided", Watermark: "中文"}
	if _, err := p.Layout(context.Background(), source, filepath.Join(dir, "out.pdf"), s, []int{1}); !errors.Is(err, printing.ErrWatermarkFont) {
		t.Fatal("missing CJK glyph was accepted", err)
	}
}
