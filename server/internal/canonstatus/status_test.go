package canonstatus

import (
	"strings"
	"testing"
)

func statusXML(status, detail string) string {
	return `<cmd xmlns:ivec="http://www.canon.com/ns/cmd/2008/07/common/"><ivec:contents>` +
		`<ivec:operation>GetStatusResponse</ivec:operation><ivec:response>OK</ivec:response>` +
		`<ivec:param_set><ivec:status>` + status + `</ivec:status><ivec:status_detail>` +
		detail + `</ivec:status_detail></ivec:param_set></ivec:contents></cmd>`
}

func TestFragmentedAndCombinedReports(t *testing.T) {
	empty := statusXML("stopped", "MediaEmpty")
	ready := statusXML("processing", "")
	for split := 0; split <= len(empty); split++ {
		var decoder Decoder
		var got []State
		emit := func(s State) { got = append(got, s) }
		decoder.Feed([]byte("noise\x00"+empty[:split]), emit)
		decoder.Feed([]byte(empty[split:]+ready), emit)
		if len(got) != 2 || got[0] != PaperEmpty || got[1] != Ready {
			t.Fatalf("split %d: %v", split, got)
		}
	}
}

func TestInvalidReportsDoNotClearPaperOut(t *testing.T) {
	for name, report := range map[string]string{
		"truncated":   strings.TrimSuffix(statusXML("idle", ""), "</cmd>"),
		"failed":      strings.Replace(statusXML("idle", ""), ">OK<", ">ERROR<", 1),
		"unrelated":   strings.Replace(statusXML("idle", ""), "GetStatusResponse", "OtherResponse", 1),
		"missing":     strings.Replace(statusXML("idle", ""), "<ivec:status>idle</ivec:status>", "", 1),
		"unknown":     statusXML("unknown", ""),
		"other error": statusXML("stopped", "MediaJam"),
		"ready error": statusXML("idle", "MediaJam"),
		"malformed":   "<cmd><ivec:status>idle</cmd>",
		"duplicate":   strings.Replace(statusXML("idle", ""), "</ivec:param_set>", "<ivec:status>idle</ivec:status></ivec:param_set>", 1),
		"too deep":    "<cmd>" + strings.Repeat("<x>", 100) + strings.Repeat("</x>", 100) + "</cmd>",
		"entity":      strings.Replace(statusXML("idle", ""), ">idle<", ">&private;<", 1),
		"wrong root":  strings.ReplaceAll(statusXML("idle", ""), "<cmd", "<other"),
	} {
		t.Run(name, func(t *testing.T) {
			var decoder Decoder
			var got []State
			decoder.Feed([]byte(report), func(s State) { got = append(got, s) })
			if len(got) != 0 {
				t.Fatalf("invalid report emitted %v", got)
			}
		})
	}
}

func TestResynchronizesAfterBrokenOrOversizedInput(t *testing.T) {
	for _, prefix := range []string{
		"<cmd><broken>",
		"<cmd>" + strings.Repeat("x", 100000),
		strings.Repeat("junk", 100000),
	} {
		var decoder Decoder
		var got []State
		emit := func(s State) { got = append(got, s) }
		for _, b := range []byte(prefix + statusXML("stopped", "MediaEmpty")) {
			decoder.Feed([]byte{b}, emit)
			if len(decoder.buffer) > maxFrame {
				t.Fatal("unbounded input buffer")
			}
		}
		if len(got) != 1 || got[0] != PaperEmpty {
			t.Fatalf("resynchronization: %v", got)
		}
	}
}
