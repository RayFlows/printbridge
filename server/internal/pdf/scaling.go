package pdf

import (
	"context"
	"math"
	"os"
	"path/filepath"

	"github.com/RayFlows/printbridge/server/internal/printing"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Normalize each source page to its own cell before imposition. Enlarging one
// page must never paint over its neighbour or change the chosen page sequence.
func scaleCells(c context.Context, source, output string, grid *model.NUp, s printing.Settings) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	boxes, err := pdfapi.Boxes(c, input, nil, pdfConfig())
	input.Close()
	if err != nil {
		return err
	}
	cell := model.DefaultNUpConfig()
	cell.PageDim = &types.Dim{Width: grid.PageDim.Width / grid.Grid.Width, Height: grid.PageDim.Height / grid.Grid.Height}
	cell.UserDim = true
	cell.Grid = &types.Dim{Width: 1, Height: 1}
	cell.Border = false
	cell.Margin = grid.Margin
	cell.Enforce = s.PagesPerSheet > 1
	fitted := filepath.Join(filepath.Dir(output), "fitted-cells.pdf")
	if err := pdfapi.NUpFile(c, []string{source}, fitted, nil, cell, pdfConfig()); err != nil {
		return err
	}
	file, err := os.Open(fitted)
	if err != nil {
		return err
	}
	defer file.Close()
	config := pdfConfig()
	config.Cmd = model.ZOOM
	doc, err := pdfapi.ReadValidateAndOptimize(c, file, config, nil)
	if err != nil {
		return err
	}
	destination := types.NewRectangle(0, 0, cell.PageDim.Width, cell.PageDim.Height)
	for i, box := range boxes {
		crop := box.CropBox()
		if crop == nil {
			return printing.ErrSettings
		}
		w, h := crop.Width(), crop.Height()
		if box.Rot%180 != 0 {
			w, h = h, w
		}
		sourceRect := types.NewRectangle(0, 0, w, h)
		fitWidth, _, _, _, rotation := types.BestFitRectIntoRect(sourceRect, destination.CroppedCopy(cell.Margin), cell.Enforce, false)
		fit := fitWidth / w
		if rotation != 0 {
			w, h = h, w
		}
		scale := math.Min((destination.Width()-2*cell.Margin)/w, (destination.Height()-2*cell.Margin)/h)
		switch s.Scaling {
		case "shrink":
			scale = math.Min(1, fit)
		case "actual":
			scale = 1
		case "custom":
			scale = float64(s.ScalePercent) / 100
		case "fill":
			scale = math.Max(destination.Width()/w, destination.Height()/h)
		}
		factor := scale / fit
		if math.IsNaN(factor) || math.IsInf(factor, 0) || factor <= 0 {
			return printing.ErrSettings
		}
		if math.Abs(factor-1) > 0.000001 {
			if err := pdfcpu.Zoom(c, doc, types.IntSet{i + 1: true}, &model.Zoom{Factor: factor}); err != nil {
				return err
			}
		}
	}
	return pdfapi.WriteContextFile(c, doc, output)
}
