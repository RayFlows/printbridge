package pdf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/RayFlows/printbridge/server/internal/printing"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func (p *Poppler) Layout(ctx context.Context, source, output string, settings printing.Settings, selected []int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp(filepath.Dir(output), "layout-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	collected := filepath.Join(dir, "selected.pdf")
	if err := collectPrintPages(ctx, source, collected, selected); err != nil {
		return 0, err
	}
	if settings.InvertImages {
		inverted := filepath.Join(dir, "inverted.pdf")
		if err := pageEffect(ctx, collected, inverted, false); err != nil {
			return 0, err
		}
		collected = inverted
	}
	width, height, err := printing.PaperDimensions(settings.Media, settings.Orientation)
	if err != nil {
		return 0, err
	}
	nup := model.DefaultNUpConfig()
	nup.PageDim = &types.Dim{Width: width, Height: height}
	nup.UserDim = true
	nup.Border = settings.PageBorder
	nup.Margin = 5 * 72 / 25.4
	if settings.PagesPerSheet == 1 {
		nup.Grid = &types.Dim{Width: 1, Height: 1}
	} else {
		if err := pdfapi.ParseNUpValue(settings.PagesPerSheet, nup); err != nil {
			return 0, err
		}
	}
	switch settings.LayoutOrder {
	case "rltb":
		nup.Orient = model.LeftDown
	case "tblr":
		nup.Orient = model.DownRight
	case "tbrl":
		nup.Orient = model.DownLeft
	}
	if settings.Scaling != "" {
		normalized := filepath.Join(dir, "scaled-cells.pdf")
		if err := scaleCells(ctx, collected, normalized, nup, settings); err != nil {
			return 0, err
		}
		collected = normalized
		nup.Margin = 0
		nup.Enforce = false
	}
	laidOut := filepath.Join(dir, "laid-out.pdf")
	if err := pdfapi.NUpFile(ctx, []string{collected}, laidOut, nil, nup, pdfConfig()); err != nil {
		return 0, err
	}
	current := laidOut
	if settings.Scaling == "" && settings.ScalePercent != 100 {
		zoomed := filepath.Join(dir, "zoomed.pdf")
		if err := pdfapi.ZoomFile(ctx, current, zoomed, nil, &model.Zoom{Factor: float64(settings.ScalePercent) / 100}, pdfConfig()); err != nil {
			return 0, err
		}
		current = zoomed
	}
	if settings.Watermark != "" {
		marked := filepath.Join(dir, "marked.pdf")
		if err := p.addWatermark(ctx, current, marked, settings.Watermark); err != nil {
			return 0, err
		}
		current = marked
	}
	if settings.Mirror {
		mirrored := filepath.Join(dir, "mirrored.pdf")
		if err := pageEffect(ctx, current, mirrored, true); err != nil {
			return 0, err
		}
		current = mirrored
	}
	count := (len(selected) + settings.PagesPerSheet - 1) / settings.PagesPerSheet
	if settings.Duplex() && count%2 != 0 {
		padded := filepath.Join(dir, "padded.pdf")
		if err := pdfapi.InsertPagesFile(ctx, current, padded, []string{strconv.Itoa(count)}, false, nil, pdfConfig()); err != nil {
			return 0, err
		}
		current = padded
		count++
	}
	if err := copyFile(current, output); err != nil {
		return 0, err
	}
	actual, err := p.Inspect(ctx, output)
	if err != nil {
		return 0, err
	}
	if actual != count {
		return 0, fmt.Errorf("layout page count differs: got %d, want %d", actual, count)
	}
	return count, nil
}

func (p *Poppler) Phase(ctx context.Context, source, output string, pages []int, rotation int) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := pdfapi.CollectFile(ctx, source, output, pageStrings(pages), pdfConfig()); err != nil {
		return err
	}
	if rotation != 0 {
		return pdfapi.RotateFile(ctx, output, "", rotation, nil, pdfConfig())
	}
	return nil
}

func pageStrings(pages []int) []string {
	values := make([]string, len(pages))
	for i, page := range pages {
		values[i] = strconv.Itoa(page)
	}
	return values
}
