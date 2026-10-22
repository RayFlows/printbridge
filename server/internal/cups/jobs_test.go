package cups_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/RayFlows/printbridge/server/internal/cups"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

func TestSubstitutedAttributesReturnTheEmptyJobForCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOkIgnoredOrSubstituted, 1)
		response.Job.Add(attr("job-id", goipp.TagInteger, goipp.Integer(42)))
		writeIPP(t, w, response)
	}))
	defer server.Close()
	client, _ := cups.New(server.URL, time.Second)
	id, err := client.CreateHeld(context.Background(), printing.Job{Settings: printing.Settings{PrinterID: "virtual"}})
	if id != 42 || !errors.Is(err, printing.ErrSettings) {
		t.Fatal(id, err)
	}
}

func TestHeldIPPJobProtocolDoesNotFilterPagesAgain(t *testing.T) {
	t.Parallel()
	var operations []goipp.Op
	job := printing.Job{ID: strings.Repeat("a", 32), Phase: "front",
		Settings: printing.Settings{PrinterID: "virtual queue", Copies: 3, Pages: "1-16", Subset: "odd",
			PagesPerSheet: 4, Reverse: true, Media: "iso_a4_210x297mm", ColorMode: "monochrome", Sides: "manual-long-edge",
			MediaType: "stationery", MediaSource: "rear", LayoutOrder: "tbrl", PageBorder: true, Scaling: "custom", ScalePercent: 40}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request goipp.Message
		if err := request.Decode(r.Body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		op := goipp.Op(request.Code)
		operations = append(operations, op)
		response := goipp.NewResponse(goipp.DefaultVersion, goipp.StatusOk, request.RequestID)
		values := func(attrs goipp.Attributes) map[string]string {
			out := map[string]string{}
			for _, a := range attrs {
				if len(a.Values) > 0 {
					out[a.Name] = a.Values[0].V.String()
				}
			}
			return out
		}
		operation := values(request.Operation)
		if uri := operation["job-uri"]; uri != "" && !strings.HasPrefix(uri, "ipp://") {
			t.Error("job attribute must use IPP URI", uri)
		}
		if uri := operation["printer-uri"]; uri != "" && !strings.HasPrefix(uri, "ipp://") {
			t.Error("printer attribute must use IPP URI", uri)
		}
		switch op {
		case goipp.OpCreateJob:
			attrs := values(request.Job)
			for key, want := range map[string]string{"copies": "1", "number-up": "1", "page-set": "all", "output-order": "normal", "sides": "one-sided", "job-hold-until": "indefinite", "media-type": "stationery", "media-source": "rear"} {
				if attrs[key] != want {
					t.Errorf("%s=%s want=%s", key, attrs[key], want)
				}
			}
			if attrs["page-ranges"] != "" || attrs["orientation-requested"] != "" || attrs["number-up-layout"] != "" || attrs["scaling"] != "" || attrs["page-border"] != "" {
				t.Error("double page transformation")
			}
			if operation["job-name"] != printing.JobName(job) {
				t.Error("unstable reconciliation name")
			}
			response.Job.Add(attr("job-id", goipp.TagInteger, goipp.Integer(42)))
		case goipp.OpSendDocument:
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != "%PDF-synthetic" {
				t.Error("lost document payload", string(body), err)
			}
			if operation["document-format"] != "application/pdf" || operation["last-document"] != "true" {
				t.Error(operation)
			}
		case goipp.OpGetJobAttributes, goipp.OpGetJobs:
			attrs := goipp.Attributes{
				attr("job-id", goipp.TagInteger, goipp.Integer(42)), attr("job-name", goipp.TagName, goipp.String(printing.JobName(job))),
				attr("job-state", goipp.TagEnum, goipp.Integer(4)), attr("number-of-documents", goipp.TagInteger, goipp.Integer(1)),
				attr("job-hold-until", goipp.TagKeyword, goipp.String("indefinite")),
			}
			if op == goipp.OpGetJobs {
				response.Groups = goipp.Groups{{Tag: goipp.TagJobGroup, Attrs: attrs}}
			} else {
				response.Job = attrs
			}
		case goipp.OpReleaseJob, goipp.OpCancelJob:
		default:
			t.Error("unexpected operation", op)
		}
		if op != goipp.OpCreateJob && op != goipp.OpGetJobs && !strings.HasSuffix(operation["job-uri"], "/jobs/42") {
			t.Error("incorrect job target", operation)
		}
		writeIPP(t, w, response)
	}))
	defer server.Close()
	client, _ := cups.New(server.URL, time.Second)
	ctx := context.Background()
	id, err := client.CreateHeld(ctx, job)
	if err != nil || id != 42 {
		t.Fatal(id, err)
	}
	path := filepath.Join(t.TempDir(), "document.pdf")
	os.WriteFile(path, []byte("%PDF-synthetic"), 0600)
	if err := client.Send(ctx, id, path); err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(ctx, id)
	if err != nil || status.Documents != 1 || status.Hold != "indefinite" {
		t.Fatal(status, err)
	}
	matches, err := client.Find(ctx, job.Settings.PrinterID, printing.JobName(job))
	if err != nil || len(matches) != 1 {
		t.Fatal(matches, err)
	}
	matches, err = client.Find(ctx, job.Settings.PrinterID, "different-name")
	if err != nil || len(matches) != 0 {
		t.Fatal(matches, err)
	}
	if err := client.Release(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := client.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(operations) != 7 {
		t.Fatal(operations)
	}
}
