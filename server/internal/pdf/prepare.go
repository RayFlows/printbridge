package pdf

import (
	"context"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/disintegration/imaging"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

type Office struct {
	Command string
	Timeout time.Duration
}

// Prepare preserves the original file and produces the PDF used by both preview and printing.
func (p *Poppler) Prepare(ctx context.Context, input, name, output string) error {
	ctx, cancel := context.WithTimeout(ctx, p.office.Timeout)
	defer cancel()
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".pdf":
		return copyFile(input, output)
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".tif", ".tiff", ".gif":
		return prepareImage(ctx, input, output)
	case ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp", ".rtf", ".txt", ".csv":
		return p.office.convert(ctx, input, ext, output)
	default:
		return documents.ErrUnsupported
	}
}

func (p *Poppler) WithOffice(command string, timeout time.Duration) *Poppler {
	p.office = Office{Command: command, Timeout: timeout}
	return p
}

func prepareImage(ctx context.Context, input, output string) error {
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	info, format, err := image.DecodeConfig(file)
	if err != nil || info.Width < 1 || info.Height < 1 || int64(info.Width)*int64(info.Height) > 40_000_000 {
		return documents.ErrConversion
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// Phone JPEGs often store orientation in EXIF rather than rotating pixels.
	if format == "jpeg" {
		img, err := imaging.Decode(file, imaging.AutoOrientation(true))
		if err != nil {
			return documents.ErrConversion
		}
		normalized, err := os.CreateTemp(filepath.Dir(output), "photo-*.png")
		if err != nil {
			return err
		}
		defer os.Remove(normalized.Name())
		err = png.Encode(normalized, img)
		if err = errors.Join(err, normalized.Close()); err != nil {
			return err
		}
		oriented, err := os.Open(normalized.Name())
		if err != nil {
			return err
		}
		defer oriented.Close()
		file = oriented
	}
	// Preserve the image aspect ratio; paper size and margins belong to Layout.
	config, err := pdfapi.Import("position:full", types.POINTS)
	if err != nil {
		return documents.ErrConversion
	}
	out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	err = pdfapi.ImportImages(ctx, nil, out, []io.Reader{file}, config, pdfConfig())
	closeErr := out.Close()
	if err != nil {
		return documents.ErrConversion
	}
	return closeErr
}

func pdfConfig() *model.Configuration {
	config := model.NewStatelessConfiguration()
	config.ValidationMode = model.ValidationRelaxed
	config.Limits.MaxImagePixels = 40_000_000
	config.Limits.MaxImageBytes = 256 << 20
	return config
}

func (o Office) convert(ctx context.Context, input, ext, output string) error {
	if o.Command == "" {
		return documents.ErrConverter
	}
	working, err := os.MkdirTemp(filepath.Dir(input), "office-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(working)
	original := filepath.Join(working, "document"+ext)
	if err := copyFile(input, original); err != nil {
		return err
	}
	profile := filepath.Join(working, "profile")
	if err := os.MkdirAll(filepath.Join(profile, "user"), 0700); err != nil {
		return err
	}
	// Use a fresh, macro-disabled profile rather than the operator's desktop session.
	const settings = `<?xml version="1.0" encoding="UTF-8"?><oor:items xmlns:oor="http://openoffice.org/2001/registry"><item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="MacroSecurityLevel" oor:op="fuse"><value>3</value></prop></item><item oor:path="/org.openoffice.Office.Common/Load"><prop oor:name="Update" oor:op="fuse"><value>0</value></prop></item></oor:items>`
	if err := os.WriteFile(filepath.Join(profile, "user", "registrymodifications.xcu"), []byte(settings), 0600); err != nil {
		return err
	}
	uriPath := filepath.ToSlash(profile)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	profileURI := (&url.URL{Scheme: "file", Path: uriPath}).String()
	command := exec.CommandContext(ctx, o.Command, "-env:UserInstallation="+profileURI, "--headless",
		"--nologo", "--nodefault", "--norestore", "--convert-to", "pdf", "--outdir", working, original)
	command.Dir = working
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.WaitDelay = time.Second
	err = command.Run()
	if ctx.Err() != nil {
		return documents.ErrTimeout
	}
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) || errors.Is(err, os.ErrNotExist) {
			return documents.ErrConverter
		}
		return documents.ErrConversion
	}
	if err := copyFile(filepath.Join(working, "document.pdf"), output); err != nil {
		return documents.ErrConversion
	}
	return nil
}

func copyFile(input, output string) error {
	in, err := os.Open(input)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, io.LimitReader(in, (128<<20)+1))
	return errors.Join(err, out.Close())
}
