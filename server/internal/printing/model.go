package printing

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RayFlows/printbridge/server/internal/documents"
	"github.com/RayFlows/printbridge/server/internal/printers"
)

var (
	ErrSettings      = errors.New("invalid or unsupported print settings")
	ErrNotFound      = errors.New("print record not found")
	ErrConflict      = errors.New("print action conflicts with current state")
	ErrActiveJob     = errors.New("this session already has an unfinished job on this printer")
	ErrBusy          = errors.New("print preparation is busy")
	ErrCapacity      = errors.New("print storage is full")
	ErrBackend       = errors.New("print backend unavailable")
	ErrWatermarkFont = errors.New("watermark font is unavailable or lacks required glyphs")
)

type Settings struct {
	PrinterID           string `json:"printerId"`
	Media               string `json:"media"`
	ColorMode           string `json:"colorMode"`
	Copies              int    `json:"copies"`
	Pages               string `json:"pages"`
	Subset              string `json:"subset"`
	Reverse             bool   `json:"reverse"`
	OutputOrder         string `json:"outputOrder,omitempty"`
	Orientation         string `json:"orientation"`
	PagesPerSheet       int    `json:"pagesPerSheet"`
	ScalePercent        int    `json:"scalePercent"`
	Sides               string `json:"sides"`
	ManualFirstSide     string `json:"manualFirstSide,omitempty"`
	ManualDuplexProfile string `json:"manualDuplexProfile,omitempty"`
	BackOrder           string `json:"backOrder"`
	BackRotation        int    `json:"backRotation"`
	Calibrated          bool   `json:"calibrated"`
	MediaType           string `json:"mediaType,omitempty"`
	MediaSource         string `json:"mediaSource,omitempty"`
	LayoutOrder         string `json:"layoutOrder,omitempty"`
	PageBorder          bool   `json:"pageBorder,omitempty"`
	Scaling             string `json:"scaling,omitempty"`
	Mirror              bool   `json:"mirror,omitempty"`
	Watermark           string `json:"watermark,omitempty"`
	InvertImages        bool   `json:"invertImages,omitempty"`
}

func (s Settings) Manual() bool { return strings.HasPrefix(s.Sides, "manual-") }
func (s Settings) Duplex() bool { return s.Sides != "one-sided" }

func (s Settings) FirstPhase() string {
	if !s.Manual() {
		return "single"
	}
	if s.ManualFirstSide == "back" {
		return "back"
	}
	return "front"
}

func (s Settings) SecondPhase() string {
	if !s.Manual() {
		return ""
	}
	if s.FirstPhase() == "back" {
		return "front"
	}
	return "back"
}

type Sheet struct {
	Front []int `json:"front"`
	Back  []int `json:"back"`
}

type Plan struct {
	ID               string    `json:"id"`
	DocumentID       string    `json:"documentId"`
	Name             string    `json:"name"`
	Owner            string    `json:"-"`
	Settings         Settings  `json:"settings"`
	SelectedPages    []int     `json:"selectedPages"`
	PageCount        int       `json:"pageCount"`
	PreviewPageCount int       `json:"previewPageCount,omitempty"`
	SheetCount       int       `json:"sheetCount"`
	Sheets           []Sheet   `json:"sheets"`
	CreatedAt        time.Time `json:"createdAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

func (p *Plan) setPreviewPageCount() {
	p.PreviewPageCount = (len(p.SelectedPages) + p.Settings.PagesPerSheet - 1) / p.Settings.PagesPerSheet
}

func (p Plan) previewPage(page int, order string) (int, error) {
	if order != "" && order != "print" && order != "reading" {
		return 0, ErrSettings
	}
	if page < 1 || page > p.PageCount {
		return 0, documents.ErrPage
	}
	if order != "reading" {
		return page, nil
	}
	if page > p.PreviewPageCount {
		return 0, documents.ErrPage
	}
	_, faces := ArrangeSheets(p.SelectedPages, p.Settings)
	physical := slices.Index(faces, page) + 1
	if physical == 0 || physical > p.PageCount {
		return 0, documents.ErrPage
	}
	return physical, nil
}

type Job struct {
	ID                   string    `json:"id"`
	PlanID               string    `json:"planId"`
	Owner                string    `json:"-"`
	Key                  string    `json:"-"`
	Name                 string    `json:"name"`
	Settings             Settings  `json:"settings"`
	SheetCount           int       `json:"sheetCount"`
	State                string    `json:"state"`
	Phase                string    `json:"phase"`
	CUPSID               int       `json:"cupsJobId"`
	CUPSState            int       `json:"cupsJobState"`
	CUPSJobIDs           []int     `json:"cupsJobIds"`
	Problem              string    `json:"problem"`
	DeviceCancelRequired bool      `json:"deviceCancelRequired,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

func (j Job) Terminal() bool {
	return slices.Contains([]string{"completed", "canceled", "failed"}, j.State)
}

func ValidateSettings(settings Settings, printer printers.Printer, pages int) ([]int, error) {
	if !utf8.ValidString(settings.Watermark) || utf8.RuneCountInString(settings.Watermark) > 80 ||
		strings.ContainsFunc(settings.Watermark, unicode.IsControl) ||
		settings.PrinterID != printer.ID || settings.Copies < 1 || settings.Copies > 99 ||
		!slices.Contains([]int{1, 2, 4, 6, 9, 16}, settings.PagesPerSheet) ||
		settings.ScalePercent < 10 || settings.ScalePercent > 400 ||
		!slices.Contains([]string{"", "lrtb", "rltb", "tblr", "tbrl"}, settings.LayoutOrder) ||
		!slices.Contains([]string{"", "fit", "shrink", "fill", "actual", "custom"}, settings.Scaling) ||
		!slices.Contains([]string{"", "normal", "reverse"}, settings.OutputOrder) ||
		!slices.Contains([]string{"", "front", "back"}, settings.ManualFirstSide) ||
		!slices.Contains([]string{"", "same", "reverse"}, settings.BackOrder) ||
		!slices.Contains([]int{0, 180}, settings.BackRotation) ||
		!slices.Contains([]string{"portrait", "landscape"}, settings.Orientation) ||
		!slices.Contains([]string{"one-sided", "two-sided-long-edge", "two-sided-short-edge", "manual-long-edge", "manual-short-edge"}, settings.Sides) {
		return nil, ErrSettings
	}
	caps := printer.Capabilities
	if settings.MediaType != "" && !slices.Contains(caps.MediaTypes, settings.MediaType) && settings.MediaType != caps.DefaultMediaType {
		return nil, ErrSettings
	}
	if settings.MediaSource != "" && !slices.Contains(caps.MediaSources, settings.MediaSource) && settings.MediaSource != caps.DefaultMediaSource {
		return nil, ErrSettings
	}
	if caps.Copies != nil && (settings.Copies < caps.Copies.Min || settings.Copies > caps.Copies.Max) {
		return nil, ErrSettings
	}
	if settings.Media != "" && !slices.Contains(caps.Media, settings.Media) && settings.Media != caps.DefaultMedia {
		return nil, ErrSettings
	}
	if settings.ColorMode != "" && !slices.Contains(caps.ColorModes, settings.ColorMode) && settings.ColorMode != caps.DefaultColorMode {
		return nil, ErrSettings
	}
	if strings.HasPrefix(settings.Sides, "two-sided") && !slices.Contains(caps.Sides, settings.Sides) {
		return nil, ErrSettings
	}
	if settings.Manual() && (!settings.Calibrated || !slices.Contains([]string{"same", "reverse"}, settings.BackOrder) ||
		(settings.BackRotation != 0 && settings.BackRotation != 180)) {
		return nil, ErrSettings
	}
	if _, _, err := PaperDimensions(settings.Media, settings.Orientation); err != nil {
		return nil, err
	}
	selected, err := SelectPages(pages, settings.Pages, settings.Subset, settings.Reverse)
	if err != nil {
		return nil, err
	}
	faces := (len(selected) + settings.PagesPerSheet - 1) / settings.PagesPerSheet
	if settings.Duplex() && faces%2 != 0 {
		faces++
	}
	if faces*settings.Copies > 2000 {
		return nil, ErrSettings
	}
	return selected, nil
}

func SelectPages(total int, ranges, subset string, reverse bool) ([]int, error) {
	if total < 1 || total > 500 || len(ranges) > 2048 || !slices.Contains([]string{"all", "odd", "even"}, subset) {
		return nil, ErrSettings
	}
	result := []int{}
	seen := map[int]bool{}
	add := func(page int) {
		if seen[page] || (subset == "odd" && page%2 == 0) || (subset == "even" && page%2 == 1) {
			return
		}
		seen[page] = true
		result = append(result, page)
	}
	if strings.TrimSpace(ranges) == "" {
		for page := 1; page <= total; page++ {
			add(page)
		}
	} else {
		for _, part := range strings.Split(ranges, ",") {
			bounds := strings.Split(strings.TrimSpace(part), "-")
			if len(bounds) > 2 {
				return nil, ErrSettings
			}
			first, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
			if err != nil {
				return nil, ErrSettings
			}
			last := first
			if len(bounds) == 2 {
				last, err = strconv.Atoi(strings.TrimSpace(bounds[1]))
				if err != nil {
					return nil, ErrSettings
				}
			}
			if first < 1 || last < first || last > total {
				return nil, ErrSettings
			}
			for page := first; page <= last; page++ {
				add(page)
			}
		}
	}
	if len(result) == 0 {
		return nil, ErrSettings
	}
	if reverse {
		slices.Reverse(result)
	}
	return result, nil
}

func SheetMap(selected []int, perSheet int, duplex bool) []Sheet {
	faces := [][]int{}
	for i := 0; i < len(selected); i += perSheet {
		face := make([]int, perSheet)
		copy(face, selected[i:min(i+perSheet, len(selected))])
		faces = append(faces, face)
	}
	sheets := []Sheet{}
	for i := 0; i < len(faces); {
		sheet := Sheet{Front: faces[i], Back: []int{}}
		i++
		if duplex {
			sheet.Back = make([]int, perSheet)
			if i < len(faces) {
				copy(sheet.Back, faces[i])
				i++
			}
		}
		sheets = append(sheets, sheet)
	}
	return sheets
}

// Reverse physical sheets after imposition, preserving each front/back pair and
// the reading order of the pages within a face.
func ArrangeSheets(selected []int, settings Settings) ([]Sheet, []int) {
	sheets := SheetMap(selected, settings.PagesPerSheet, settings.Duplex())
	ordered := make([]Sheet, 0, len(sheets))
	faces := []int{}
	for i := range sheets {
		index := i
		if settings.OutputOrder == "reverse" {
			index = len(sheets) - 1 - i
		}
		ordered = append(ordered, sheets[index])
		if settings.Duplex() {
			faces = append(faces, 2*index+1, 2*index+2)
		} else {
			faces = append(faces, index+1)
		}
	}
	return ordered, faces
}

// PhasePages selects from the already laid-out, blank-padded PDF exactly once.
func PhasePages(pageCount int, settings Settings, phase string) []int {
	pages := []int{}
	for copyIndex := 0; copyIndex < settings.Copies; copyIndex++ {
		for page := 1; page <= pageCount; page++ {
			if phase == "front" && page%2 == 0 || phase == "back" && page%2 == 1 {
				continue
			}
			pages = append(pages, page)
		}
	}
	if phase == settings.SecondPhase() && settings.BackOrder == "reverse" {
		slices.Reverse(pages)
	}
	return pages
}

var mediaSize = regexp.MustCompile(`_([0-9.]+)x([0-9.]+)(mm|in)$`)
var ppdSize = regexp.MustCompile(`^w([0-9.]+)h([0-9.]+)(?:j)?$`)

func PaperDimensions(media, orientation string) (float64, float64, error) {
	width, height := 210.0, 297.0
	switch strings.ToLower(media) {
	case "", "a4", "iso_a4_210x297mm":
	case "a5":
		width, height = 148, 210
	case "a6":
		width, height = 105, 148
	case "a7":
		width, height = 74, 105
	case "a8":
		width, height = 52, 74
	case "a9":
		width, height = 37, 52
	case "isob5":
		width, height = 176, 250
	case "isob6":
		width, height = 125, 176
	case "b5":
		width, height = 182, 257
	case "b6":
		width, height = 128, 182
	case "executive":
		width, height = 184.15, 266.7
	case "statement":
		width, height = 139.7, 215.9
	case "postcard":
		width, height = 100, 148
	case "c5":
		width, height = 162, 229
	case "c6", "c6_l":
		width, height = 114, 162
	case "dl":
		width, height = 110, 220
	case "com10":
		width, height = 104.775, 241.3
	case "monarch", "monarch_l":
		width, height = 98.425, 190.5
	case "letter":
		width, height = 215.9, 279.4
	case "legal":
		width, height = 215.9, 355.6
	default:
		if match := ppdSize.FindStringSubmatch(strings.ToLower(media)); match != nil {
			w, err := strconv.ParseFloat(match[1], 64)
			if err != nil {
				return 0, 0, ErrSettings
			}
			h, err := strconv.ParseFloat(match[2], 64)
			if err != nil {
				return 0, 0, ErrSettings
			}
			// PPD custom names are expressed in PostScript points.
			width, height = w*25.4/72, h*25.4/72
			break
		}
		match := mediaSize.FindStringSubmatch(media)
		if match == nil {
			return 0, 0, ErrSettings
		}
		var err error
		width, err = strconv.ParseFloat(match[1], 64)
		if err != nil {
			return 0, 0, ErrSettings
		}
		height, err = strconv.ParseFloat(match[2], 64)
		if err != nil {
			return 0, 0, ErrSettings
		}
		if match[3] == "in" {
			width *= 25.4
			height *= 25.4
		}
	}
	if width < 30 || height < 30 || width > 1200 || height > 1200 {
		return 0, 0, ErrSettings
	}
	if width > height {
		width, height = height, width
	}
	if orientation == "landscape" {
		width, height = height, width
	}
	return width * 72 / 25.4, height * 72 / 25.4, nil
}
