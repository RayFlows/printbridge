package pdf

import (
	"context"
	"os"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/fault"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

func collectPrintPages(ctx context.Context, source, output string, pages []int) (err error) {
	defer fault.Catch(&err)
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	config := pdfConfig()
	config.Cmd = model.COLLECT
	doc, err := pdfapi.ReadValidateAndOptimize(ctx, input, config, nil)
	if err != nil {
		return err
	}
	// Imposition replaces page objects. Carrying named destinations into the
	// print derivative leaves stale references that break later extraction.
	// Detach the cache without recursively deleting objects shared by pages.
	delete(doc.Names, "Dests")
	doc.Dests = nil
	collected, err := pdfcpu.ExtractPages(ctx, doc, pages, false)
	if err != nil {
		return err
	}
	return pdfapi.WriteContextFile(ctx, collected, output)
}
