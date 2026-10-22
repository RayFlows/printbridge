package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/RayFlows/printbridge/server/internal/printing"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Imported image pages are inverted before fitting, so paper margins and the
// padding face stay white. Mirroring applies to the final composed paper face.
func pageEffect(c context.Context, source, output string, mirror bool) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	doc, err := pdfapi.ReadValidateAndOptimize(c, file, pdfConfig(), nil)
	if err != nil {
		return err
	}
	for page := 1; page <= doc.PageCount; page++ {
		if err := c.Err(); err != nil {
			return err
		}
		d, _, attrs, err := doc.PageDict(c, page, false)
		if err != nil {
			return err
		}
		box := attrs.MediaBox
		if attrs.CropBox != nil {
			box = attrs.CropBox
		}
		content, err := doc.PageContent(d, page)
		if err != nil && !errors.Is(err, model.ErrNoContent) {
			return err
		}
		var b bytes.Buffer
		if mirror {
			if attrs.Rotate != 0 {
				return printing.ErrSettings
			}
			fmt.Fprintf(&b, "q -1 0 0 1 %.5f 0 cm\n", box.LL.X+box.UR.X)
			b.Write(content)
			b.WriteString("\nQ\n")
		} else {
			resources := types.NewDict()
			if attrs.Resources != nil {
				resources = attrs.Resources.Clone().(types.Dict)
			}
			states, err := doc.DereferenceDict(resources["ExtGState"])
			if err != nil {
				return err
			}
			if states == nil {
				states = types.NewDict()
			} else {
				states = states.Clone().(types.Dict)
			}
			name := "PBInvert"
			for states[name] != nil {
				name += "X"
			}
			states[name] = types.Dict{"Type": types.Name("ExtGState"), "BM": types.Name("Difference")}
			resources["ExtGState"] = states
			d["Resources"] = resources
			b.WriteString("q\n")
			b.Write(content)
			fmt.Fprintf(&b, "\nQ\nq /%s gs 1 1 1 rg %.5f %.5f %.5f %.5f re f Q\n",
				name, box.LL.X, box.LL.Y, box.Width(), box.Height())
		}
		stream, err := doc.NewStreamDictForBuf(b.Bytes())
		if err != nil {
			return err
		}
		if err := stream.Encode(); err != nil {
			return err
		}
		ref, err := doc.IndRefForNewObject(*stream)
		if err != nil {
			return err
		}
		d["Contents"] = *ref
	}
	return pdfapi.WriteContextFile(c, doc, output)
}

func (p *Poppler) loadWatermarkFont() (*opentype.Font, error) {
	p.fontOnce.Do(func() {
		var data []byte
		if p.fontPath != "" {
			data, p.fontError = os.ReadFile(p.fontPath)
			if p.fontError != nil {
				return
			}
		} else {
			candidates := []string{"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"}
			switch runtime.GOOS {
			case "windows":
				candidates = []string{filepath.Join(os.Getenv("WINDIR"), "Fonts", "msyh.ttc")}
			case "darwin":
				candidates = []string{"/System/Library/Fonts/PingFang.ttc"}
			}
			for _, path := range candidates {
				if b, err := os.ReadFile(path); err == nil {
					data = b
					break
				}
			}
			if len(data) == 0 {
				data = goregular.TTF
			}
		}
		collection, err := opentype.ParseCollection(data)
		if err == nil {
			p.watermarkFont, p.fontError = collection.Font(0)
			for i := 0; i < collection.NumFonts(); i++ {
				f, err := collection.Font(i)
				if err != nil {
					continue
				}
				name, err := f.Name(nil, sfnt.NameIDFamily)
				if err == nil && (strings.Contains(name, "CJK SC") || strings.Contains(name, "PingFang SC")) {
					p.watermarkFont, p.fontError = f, nil
					break
				}
			}
		} else {
			p.watermarkFont, p.fontError = opentype.Parse(data)
		}
	})
	if p.fontError != nil || p.watermarkFont == nil {
		return nil, printing.ErrWatermarkFont
	}
	return p.watermarkFont, nil
}

func (p *Poppler) addWatermark(c context.Context, source, output, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return copyFile(source, output)
	}
	f, err := p.loadWatermarkFont()
	if err != nil {
		return err
	}
	for _, r := range text {
		index, err := f.GlyphIndex(nil, r)
		if err != nil || index == 0 {
			return printing.ErrWatermarkFont
		}
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 96, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return printing.ErrWatermarkFont
	}
	bounds, _ := font.BoundString(face, text)
	extent := max((bounds.Max.X - bounds.Min.X).Ceil(), (bounds.Max.Y - bounds.Min.Y).Ceil())
	face.Close()
	if extent < 1 {
		return printing.ErrSettings
	}
	// Keep even a one-character stamp sharp at printing scale without allocating
	// a large image for a long line or an unusually narrow glyph.
	face, err = opentype.NewFace(f, &opentype.FaceOptions{Size: 96 * 1600 / float64(extent), DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return printing.ErrWatermarkFont
	}
	defer face.Close()
	bounds, _ = font.BoundString(face, text)
	width, height := (bounds.Max.X-bounds.Min.X).Ceil()+8, (bounds.Max.Y-bounds.Min.Y).Ceil()+8
	if width < 1 || height < 1 || width*height > 4_000_000 {
		return printing.ErrSettings
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	drawer := font.Drawer{Dst: img, Src: image.NewUniform(color.NRGBA{R: 64, G: 64, B: 64, A: 255}),
		Face: face, Dot: fixed.Point26_6{X: fixed.I(4) - bounds.Min.X, Y: fixed.I(4) - bounds.Min.Y}}
	drawer.DrawString(text)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return err
	}
	// An image stamp avoids pdfcpu's process-global user-font registry and embeds
	// only the requested glyphs as pixels, including CJK, into the immutable PDF.
	return pdfapi.AddImageWatermarksForReaderFile(c, source, output, nil, true, &b,
		"position:c, rotation:35, scale:0.7 relative, opacity:0.2", pdfConfig())
}
