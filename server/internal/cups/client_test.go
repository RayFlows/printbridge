package cups_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/RayFlows/printbridge/server/internal/cups"
	"github.com/RayFlows/printbridge/server/internal/printers"
)

func TestListMultiplePrintersAndCapabilities(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request goipp.Message
		if err := request.Decode(r.Body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/" || r.Header.Get("Content-Type") != goipp.ContentType ||
			request.Code != goipp.Code(goipp.OpCupsGetPrinters) || len(request.Job) != 0 {
			t.Error("request must only list queues, without print-job attributes")
		}
		var names []string
		for _, attr := range request.Operation {
			if attr.Name == "requested-attributes" {
				for _, value := range attr.Values {
					names = append(names, value.V.String())
				}
			}
		}
		if !strings.Contains(strings.Join(names, ","), "sides-supported") {
			t.Error("capabilities were not requested")
		}
		response := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, request.RequestID)
		response.Groups = goipp.Groups{
			{Tag: goipp.TagPrinterGroup, Attrs: goipp.Attributes{
				attr("printer-name", goipp.TagName, goipp.String("Z-queue")),
				attr("printer-info", goipp.TagTextLang, goipp.TextWithLang{Lang: "en", Text: "Office printer"}),
				attr("printer-state", goipp.TagEnum, goipp.Integer(5)),
				attr("printer-is-accepting-jobs", goipp.TagBoolean, goipp.Boolean(false)),
				attr("printer-state-reasons", goipp.TagKeyword, goipp.String("media-empty-warning")),
				attr("printer-state-message", goipp.TagText, goipp.String("Synthetic detail")),
				attr("media-supported", goipp.TagKeyword, goipp.String("iso_a4_210x297mm")),
				attr("media-type-supported", goipp.TagKeyword, goipp.String("stationery")),
				attr("media-type-default", goipp.TagKeyword, goipp.String("stationery")),
				attr("media-source-supported", goipp.TagKeyword, goipp.String("rear")),
				attr("media-source-default", goipp.TagKeyword, goipp.String("rear")),
				attr("sides-supported", goipp.TagKeyword, goipp.String("one-sided")),
				attr("copies-supported", goipp.TagRange, goipp.Range{Lower: 1, Upper: 99}),
			}},
			{Tag: goipp.TagPrinterGroup, Attrs: goipp.Attributes{
				attr("printer-name", goipp.TagName, goipp.String("A-queue")),
			}},
		}
		writeIPP(t, w, response)
	}))
	defer server.Close()
	client, err := cups.New(server.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	list, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "A-queue" || list[1].ID != "Z-queue" {
		t.Fatalf("incorrect group isolation or ordering: %#v", list)
	}
	unknown := list[0]
	if unknown.Name != unknown.ID || unknown.State != "unknown" || unknown.AcceptingJobs != nil ||
		unknown.Capabilities.Copies != nil || unknown.Capabilities.Sides == nil || len(unknown.Capabilities.Sides) != 0 {
		t.Fatalf("unknown capabilities were invented: %#v", unknown)
	}
	known := list[1]
	if !reflect.DeepEqual(known.Capabilities.MediaTypes, []string{"stationery"}) ||
		!reflect.DeepEqual(known.Capabilities.MediaSources, []string{"rear"}) ||
		known.Capabilities.DefaultMediaType != "stationery" || known.Capabilities.DefaultMediaSource != "rear" ||
		len(unknown.Capabilities.MediaTypes) != 0 || len(unknown.Capabilities.MediaSources) != 0 {
		t.Fatal("media capabilities were lost or invented")
	}
	if known.State != "stopped" || known.StateMessage != "Synthetic detail" || known.AcceptingJobs == nil || *known.AcceptingJobs ||
		known.Name != "Office printer" || known.Capabilities.Copies.Max != 99 ||
		!reflect.DeepEqual(known.Capabilities.Sides, []string{"one-sided"}) {
		t.Fatalf("incorrect capabilities: %#v", known)
	}
}

func TestEmptyQueues(t *testing.T) {
	t.Parallel()
	for _, status := range []goipp.Status{goipp.StatusOk, goipp.StatusErrorNotFound} {
		t.Run(status.String(), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeIPP(t, w, goipp.NewResponse(goipp.DefaultVersion, status, 1))
			}))
			defer server.Close()
			client, _ := cups.New(server.URL, time.Second)
			list, err := client.List(context.Background())
			if err != nil || list == nil || len(list) != 0 {
				t.Fatalf("empty queues: %#v, %v", list, err)
			}
		})
	}
}

func TestUnavailableAndMalformedResponses(t *testing.T) {
	t.Parallel()
	tests := map[string]http.HandlerFunc{
		"HTTP error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
		"HTML": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>login</html>"))
		},
		"broken IPP": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", goipp.ContentType)
			_, _ = w.Write([]byte("invalid"))
		},
		"mismatched request": func(w http.ResponseWriter, r *http.Request) {
			writeIPP(t, w, goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, 2))
		},
		"IPP error": func(w http.ResponseWriter, r *http.Request) {
			writeIPP(t, w, goipp.NewResponse(goipp.DefaultVersion, goipp.StatusErrorForbidden, 1))
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		},
		"too large": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", goipp.ContentType)
			_, _ = w.Write([]byte(strings.Repeat("x", (4<<20)+1)))
		},
		"missing identifier": func(w http.ResponseWriter, r *http.Request) {
			response := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, 1)
			response.Printer.Add(attr("printer-info", goipp.TagText, goipp.String("no identifier")))
			writeIPP(t, w, response)
		},
		"duplicate identifier": func(w http.ResponseWriter, r *http.Request) {
			response := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, 1)
			group := goipp.Group{Tag: goipp.TagPrinterGroup, Attrs: goipp.Attributes{
				attr("printer-name", goipp.TagName, goipp.String("same")),
			}}
			response.Groups = goipp.Groups{group, group}
			writeIPP(t, w, response)
		},
	}
	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(handler)
			defer server.Close()
			client, _ := cups.New(server.URL, time.Second)
			list, err := client.List(context.Background())
			if list != nil || !errors.Is(err, printers.ErrUnavailable) {
				t.Fatalf("invalid backend response was accepted: %#v, %v", list, err)
			}
		})
	}
}

func TestRequestDeadlineAndConnectionFailure(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	client, _ := cups.New(server.URL, 30*time.Millisecond)
	if _, err := client.List(context.Background()); !errors.Is(err, printers.ErrUnavailable) {
		t.Fatalf("timeout not reported: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.List(ctx); !errors.Is(err, printers.ErrUnavailable) {
		t.Fatalf("cancellation not reported: %v", err)
	}
	closedServer := httptest.NewServer(http.NotFoundHandler())
	closedClient, _ := cups.New(closedServer.URL, time.Second)
	closedServer.Close()
	if _, err := closedClient.List(context.Background()); !errors.Is(err, printers.ErrUnavailable) {
		t.Fatalf("connection failure not reported: %v", err)
	}
}

func attr(name string, tag goipp.Tag, value goipp.Value) goipp.Attribute {
	return goipp.MakeAttribute(name, tag, value)
}

func writeIPP(t *testing.T, w http.ResponseWriter, response *goipp.Message) {
	t.Helper()
	w.Header().Set("Content-Type", goipp.ContentType)
	if err := response.Encode(w); err != nil {
		t.Error(err)
	}
}
