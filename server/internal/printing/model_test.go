package printing_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/RayFlows/printbridge/server/internal/printers"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

func TestWatermarkSettingsAreBoundedAndPreserveLegacyDefaults(t *testing.T) {
	for _, text := range []string{"", "内部资料 / Draft (100%): x,y", strings.Repeat("水", 80)} {
		s := settings()
		s.Mirror, s.Watermark = true, text
		if _, err := printing.ValidateSettings(s, printer(), 3); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{strings.Repeat("水", 81), "a\nb", "a\x00b", string([]byte{255})} {
		s := settings()
		s.Watermark = text
		if _, err := printing.ValidateSettings(s, printer(), 3); !errors.Is(err, printing.ErrSettings) {
			t.Fatal("invalid watermark accepted", err)
		}
	}
}

func settings() printing.Settings {
	return printing.Settings{PrinterID: "virtual", Media: "iso_a4_210x297mm", ColorMode: "monochrome",
		Copies: 1, Subset: "all", Orientation: "portrait", PagesPerSheet: 1, ScalePercent: 100, Sides: "one-sided", BackOrder: "same"}
}
func printer() printers.Printer {
	return printers.Printer{ID: "virtual", Capabilities: printers.Capabilities{
		Media: []string{"iso_a4_210x297mm"}, ColorModes: []string{"monochrome"}, Sides: []string{"one-sided", "two-sided-long-edge"}}}
}
func TestManualPairingIncludesBlankAndCopies(t *testing.T) {
	for _, count := range []int{1, 2, 3, 16, 17} {
		selected, err := printing.SelectPages(count, "", "all", false)
		if err != nil {
			t.Fatal(err)
		}
		sheets := printing.SheetMap(selected, 1, true)
		if len(sheets) != (count+1)/2 {
			t.Fatal(sheets)
		}
		for i, sheet := range sheets {
			wantBack := 2*i + 2
			if wantBack > count {
				wantBack = 0
			}
			if sheet.Front[0] != 2*i+1 || sheet.Back[0] != wantBack {
				t.Fatalf("%d: incorrect pair %+v", count, sheet)
			}
		}
		faces := len(sheets) * 2
		for _, copies := range []int{1, 2, 3} {
			s := settings()
			s.Copies, s.Sides = copies, "manual-long-edge"
			front := printing.PhasePages(faces, s, "front")
			for _, order := range []string{"same", "reverse"} {
				s.BackOrder = order
				back := printing.PhasePages(faces, s, "back")
				for i, f := range front {
					j := i
					if order == "reverse" {
						j = len(back) - 1 - i
					}
					if back[j] != f+1 {
						t.Fatalf("wrong physical pair %d/%d for %d copies %s", f, back[j], copies, order)
					}
				}
			}
		}
	}
}
func TestPageSelectionLayoutAndReversalAreAppliedOnce(t *testing.T) {
	selected, err := printing.SelectPages(16, "", "odd", false)
	if err != nil || !reflect.DeepEqual(selected, []int{1, 3, 5, 7, 9, 11, 13, 15}) {
		t.Fatal(selected, err)
	}
	selected, err = printing.SelectPages(17, "2-4,9,17,3", "all", true)
	if err != nil || !reflect.DeepEqual(selected, []int{17, 9, 4, 3, 2}) {
		t.Fatal(selected, err)
	}
	want := []printing.Sheet{{Front: []int{17, 9}, Back: []int{4, 3}}, {Front: []int{2, 0}, Back: []int{0, 0}}}
	if got := printing.SheetMap(selected, 2, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	for _, rangeValue := range []string{"0", "18", "3-1", "1-", "-3", "1,,2", "1-a"} {
		if _, err := printing.SelectPages(17, rangeValue, "all", false); !errors.Is(err, printing.ErrSettings) {
			t.Fatal(rangeValue, err)
		}
	}
	if _, err := printing.SelectPages(1, "", "even", false); !errors.Is(err, printing.ErrSettings) {
		t.Fatal(err)
	}
}

func TestEvenFirstOutputPreservesBlankFacesCopiesAndPairing(t *testing.T) {
	for _, total := range []int{1, 2, 3, 4, 16, 17} {
		for _, outputOrder := range []string{"normal", "reverse"} {
			for _, firstSide := range []string{"", "front", "back"} {
				for _, order := range []string{"same", "reverse"} {
					for _, copies := range []int{1, 2, 3} {
						s := settings()
						s.Sides, s.ManualFirstSide, s.OutputOrder = "manual-long-edge", firstSide, outputOrder
						s.BackOrder, s.Copies = order, copies
						selected, _ := printing.SelectPages(total, "", "all", false)
						_, layoutFaces := printing.ArrangeSheets(selected, s)
						first := printing.PhasePages(len(layoutFaces), s, s.FirstPhase())
						second := printing.PhasePages(len(layoutFaces), s, s.SecondPhase())
						sheetCount := (total + 1) / 2
						if len(first) != sheetCount*copies || len(second) != len(first) {
							t.Fatal("missing blank face or copy", total, first, second)
						}
						for i, face := range first {
							sheet := i % sheetCount
							if outputOrder == "reverse" {
								sheet = sheetCount - 1 - sheet
							}
							wantFirst, wantSecond := sheet*2+1, sheet*2+2
							if firstSide == "back" {
								wantFirst, wantSecond = wantSecond, wantFirst
							}
							j := i
							if order == "reverse" {
								j = len(second) - 1 - i
							}
							if layoutFaces[face-1] != wantFirst || layoutFaces[second[j]-1] != wantSecond {
								t.Fatalf("total=%d output=%s first=%s order=%s copies=%d: incorrect physical pair",
									total, outputOrder, firstSide, order, copies)
							}
						}
					}
				}
			}
		}
	}
	// With n-up/ranges, odd/even refer to imposed faces, not original page numbers.
	s := settings()
	s.Sides, s.ManualFirstSide, s.OutputOrder, s.PagesPerSheet = "manual-long-edge", "back", "reverse", 2
	sheets, _ := printing.ArrangeSheets([]int{2, 3, 4, 9, 17}, s)
	got := [][][]int{}
	for _, phase := range []string{s.FirstPhase(), s.SecondPhase()} {
		faces := [][]int{}
		for _, page := range printing.PhasePages(len(sheets)*2, s, phase) {
			sheet := sheets[(page-1)/2]
			if page%2 == 0 {
				faces = append(faces, sheet.Back)
			} else {
				faces = append(faces, sheet.Front)
			}
		}
		got = append(got, faces)
	}
	want := [][][]int{{{0, 0}, {4, 9}}, {{17, 0}, {2, 3}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("n-up or range was filtered twice", got)
	}
}
func TestSettingsRespectCapabilitiesAndRequireCalibration(t *testing.T) {
	base := settings()
	if _, err := printing.ValidateSettings(base, printer(), 16); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*printing.Settings){
		func(s *printing.Settings) { s.Copies = 0 }, func(s *printing.Settings) { s.Copies = 100 },
		func(s *printing.Settings) { s.Media = "unknown" }, func(s *printing.Settings) { s.ColorMode = "color" },
		func(s *printing.Settings) { s.Sides = "two-sided-short-edge" }, func(s *printing.Settings) { s.Sides = "manual-long-edge" },
		func(s *printing.Settings) { s.PagesPerSheet = 3 }, func(s *printing.Settings) { s.ScalePercent = 401 },
		func(s *printing.Settings) { s.ScalePercent = 9 }, func(s *printing.Settings) { s.Scaling = "unknown" },
		func(s *printing.Settings) { s.LayoutOrder = "unknown" }, func(s *printing.Settings) { s.MediaType = "photo" },
		func(s *printing.Settings) { s.MediaSource = "rear" },
		func(s *printing.Settings) { s.OutputOrder = "unknown" },
		func(s *printing.Settings) { s.ManualFirstSide = "unknown" },
		func(s *printing.Settings) { s.BackOrder = "invalid"; s.Sides = "manual-long-edge"; s.Calibrated = true },
	} {
		s := base
		mutate(&s)
		if _, err := printing.ValidateSettings(s, printer(), 16); !errors.Is(err, printing.ErrSettings) {
			t.Fatal(s, err)
		}
	}
	base.Sides = "manual-long-edge"
	base.Calibrated = true
	if _, err := printing.ValidateSettings(base, printer(), 17); err != nil {
		t.Fatal(err)
	}
}

func TestReverseSheetOrderPreservesDuplexPairsAndNUp(t *testing.T) {
	for _, tt := range []struct {
		name     string
		selected []int
		nup      int
		sides    string
		sheets   []printing.Sheet
		faces    []int
	}{
		{"simplex", []int{1, 2, 3}, 1, "one-sided",
			[]printing.Sheet{{Front: []int{3}, Back: []int{}}, {Front: []int{2}, Back: []int{}}, {Front: []int{1}, Back: []int{}}}, []int{3, 2, 1}},
		{"odd manual", []int{1, 2, 3}, 1, "manual-long-edge",
			[]printing.Sheet{{Front: []int{3}, Back: []int{0}}, {Front: []int{1}, Back: []int{2}}}, []int{3, 4, 1, 2}},
		{"even automatic", []int{1, 2, 3, 4}, 1, "two-sided-long-edge",
			[]printing.Sheet{{Front: []int{3}, Back: []int{4}}, {Front: []int{1}, Back: []int{2}}}, []int{3, 4, 1, 2}},
		{"nup range", []int{2, 3, 4, 9, 17}, 2, "manual-short-edge",
			[]printing.Sheet{{Front: []int{17, 0}, Back: []int{0, 0}}, {Front: []int{2, 3}, Back: []int{4, 9}}}, []int{3, 4, 1, 2}},
		{"simplex nup", []int{1, 2, 3, 4, 5}, 2, "one-sided",
			[]printing.Sheet{{Front: []int{5, 0}, Back: []int{}}, {Front: []int{3, 4}, Back: []int{}}, {Front: []int{1, 2}, Back: []int{}}}, []int{3, 2, 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := settings()
			s.PagesPerSheet, s.Sides, s.OutputOrder = tt.nup, tt.sides, "reverse"
			sheets, faces := printing.ArrangeSheets(tt.selected, s)
			if !reflect.DeepEqual(sheets, tt.sheets) || !reflect.DeepEqual(faces, tt.faces) {
				t.Fatalf("sheets=%+v faces=%v", sheets, faces)
			}
		})
	}
	for _, total := range []int{1, 2, 3, 16, 17} {
		s := settings()
		s.Sides, s.OutputOrder = "manual-long-edge", "reverse"
		pages, _ := printing.SelectPages(total, "", "all", false)
		sheets, _ := printing.ArrangeSheets(pages, s)
		for i, sheet := range sheets {
			front := (len(sheets)-1-i)*2 + 1
			back := front + 1
			if back > total {
				back = 0
			}
			if sheet.Front[0] != front || sheet.Back[0] != back {
				t.Fatalf("%d pages: %+v", total, sheets)
			}
		}
		for _, copies := range []int{1, 2, 3} {
			for _, order := range []string{"same", "reverse"} {
				s.Copies, s.BackOrder = copies, order
				fronts := printing.PhasePages(len(sheets)*2, s, "front")
				backs := printing.PhasePages(len(sheets)*2, s, "back")
				for i, face := range fronts {
					backIndex := i
					if order == "reverse" {
						backIndex = len(backs) - 1 - i
					}
					sheet := sheets[(face-1)/2]
					backSheet := sheets[(backs[backIndex]-1)/2]
					if !reflect.DeepEqual(sheet, backSheet) {
						t.Fatalf("copies=%d order=%s: mismatched physical pair", copies, order)
					}
				}
			}
		}
	}
}

func TestOptionalDeviceAndLayoutOptions(t *testing.T) {
	p := printer()
	p.Capabilities.MediaTypes = []string{"stationery", "photographic-glossy"}
	p.Capabilities.MediaSources = []string{"main", "rear"}
	s := settings()
	s.MediaType, s.MediaSource = "photographic-glossy", "rear"
	s.PageBorder = true
	for _, order := range []string{"lrtb", "rltb", "tblr", "tbrl"} {
		s.LayoutOrder = order
		for _, mode := range []string{"fit", "shrink", "actual", "fill", "custom"} {
			s.Scaling = mode
			for _, scale := range []int{10, 40, 100, 400} {
				s.ScalePercent = scale
				if _, err := printing.ValidateSettings(s, p, 4); err != nil {
					t.Fatal(order, mode, scale, err)
				}
			}
		}
	}
}
