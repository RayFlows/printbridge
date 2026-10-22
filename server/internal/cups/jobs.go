package cups

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/OpenPrinting/goipp"
	"github.com/RayFlows/printbridge/server/internal/printing"
)

func (c *Client) jobRequest(op goipp.Op, path string) *goipp.Message {
	request := goipp.NewRequest(goipp.DefaultVersion, op, 1)
	request.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	request.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en")))
	name := "printer-uri"
	if strings.HasPrefix(path, "/jobs/") {
		name = "job-uri"
	}
	target, _ := url.Parse(strings.TrimRight(c.endpoint, "/") + path)
	if target.Scheme == "https" {
		target.Scheme = "ipps"
	} else {
		target.Scheme = "ipp"
	}
	request.Operation.Add(goipp.MakeAttribute(name, goipp.TagURI, goipp.String(target.String())))
	request.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName, goipp.String("printbridge")))
	return request
}

func (c *Client) perform(ctx context.Context, path string, message *goipp.Message, document io.Reader, size int64) (*goipp.Message, error) {
	data, err := message.EncodeBytes()
	if err != nil {
		return nil, printing.ErrBackend
	}
	var body io.Reader = bytes.NewReader(data)
	if document != nil {
		body = io.MultiReader(body, document)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.endpoint, "/")+path, body)
	if err != nil {
		return nil, printing.ErrBackend
	}
	request.ContentLength = int64(len(data)) + size
	request.Header.Set("Content-Type", goipp.ContentType)
	request.Header.Set("Accept", goipp.ContentType)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, printing.ErrBackend
	}
	defer response.Body.Close()
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != 200 || err != nil || contentType != goipp.ContentType {
		return nil, printing.ErrBackend
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(payload) > maxResponseBytes {
		return nil, printing.ErrBackend
	}
	var result goipp.Message
	if err := result.DecodeBytes(payload); err != nil || result.RequestID != message.RequestID {
		return nil, printing.ErrBackend
	}
	if goipp.Status(result.Code) == goipp.StatusErrorNotFound {
		return nil, printing.ErrNotFound
	}
	if result.Code >= 0x0100 {
		return nil, printing.ErrBackend
	}
	// A substituted hold/duplex attribute could cause unconfirmed output.
	if result.Code != goipp.Code(goipp.StatusOk) && message.Code == goipp.Code(goipp.OpCreateJob) {
		return &result, printing.ErrSettings
	}
	return &result, nil
}

func (c *Client) CreateHeld(ctx context.Context, job printing.Job) (int, error) {
	path := "/printers/" + url.PathEscape(job.Settings.PrinterID)
	request := c.jobRequest(goipp.OpCreateJob, path)
	request.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName, goipp.String(printing.JobName(job))))
	request.Operation.Add(goipp.MakeAttribute("ipp-attribute-fidelity", goipp.TagBoolean, goipp.Boolean(true)))
	request.Job.Add(goipp.MakeAttribute("job-hold-until", goipp.TagKeyword, goipp.String("indefinite")))
	request.Job.Add(goipp.MakeAttribute("copies", goipp.TagInteger, goipp.Integer(1)))
	request.Job.Add(goipp.MakeAttribute("number-up", goipp.TagInteger, goipp.Integer(1)))
	request.Job.Add(goipp.MakeAttribute("output-order", goipp.TagKeyword, goipp.String("normal")))
	request.Job.Add(goipp.MakeAttribute("page-set", goipp.TagKeyword, goipp.String("all")))
	sides := job.Settings.Sides
	if job.Settings.Manual() {
		sides = "one-sided"
	}
	request.Job.Add(goipp.MakeAttribute("sides", goipp.TagKeyword, goipp.String(sides)))
	if job.Settings.Media != "" {
		request.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String(job.Settings.Media)))
	}
	if job.Settings.ColorMode != "" {
		request.Job.Add(goipp.MakeAttribute("print-color-mode", goipp.TagKeyword, goipp.String(job.Settings.ColorMode)))
	}
	if job.Settings.MediaType != "" {
		request.Job.Add(goipp.MakeAttribute("media-type", goipp.TagKeyword, goipp.String(job.Settings.MediaType)))
	}
	if job.Settings.MediaSource != "" {
		request.Job.Add(goipp.MakeAttribute("media-source", goipp.TagKeyword, goipp.String(job.Settings.MediaSource)))
	}
	response, err := c.perform(ctx, path, request, nil, 0)
	if err != nil {
		if response != nil {
			return max(0, integer(response.Job, "job-id", 0)), err
		}
		return 0, err
	}
	id := integer(response.Job, "job-id", -1)
	if id < 1 {
		return 0, printing.ErrBackend
	}
	return id, nil
}

func (c *Client) Send(ctx context.Context, id int, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	target := "/jobs/" + strconv.Itoa(id)
	request := c.jobRequest(goipp.OpSendDocument, target)
	request.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType, goipp.String("application/pdf")))
	request.Operation.Add(goipp.MakeAttribute("last-document", goipp.TagBoolean, goipp.Boolean(true)))
	_, err = c.perform(ctx, target, request, file, info.Size())
	return err
}

func (c *Client) Release(ctx context.Context, id int) error {
	path := "/jobs/" + strconv.Itoa(id)
	_, err := c.perform(ctx, path, c.jobRequest(goipp.OpReleaseJob, path), nil, 0)
	return err
}
func (c *Client) Cancel(ctx context.Context, id int) error {
	path := "/jobs/" + strconv.Itoa(id)
	_, err := c.perform(ctx, path, c.jobRequest(goipp.OpCancelJob, path), nil, 0)
	return err
}
func requestedJobAttributes(request *goipp.Message) {
	values := goipp.Values{}
	for _, name := range []string{"job-id", "job-name", "job-state", "number-of-documents", "job-hold-until"} {
		values.Add(goipp.TagKeyword, goipp.String(name))
	}
	request.Operation.Add(goipp.Attribute{Name: "requested-attributes", Values: values})
}
func parseJob(attrs goipp.Attributes) (printing.Status, error) {
	status := printing.Status{ID: integer(attrs, "job-id", -1), Name: firstString(attrs, "job-name"),
		State: integer(attrs, "job-state", -1), Documents: integer(attrs, "number-of-documents", -1),
		Hold: firstString(attrs, "job-hold-until")}
	if status.ID < 1 || status.Name == "" || status.State < 3 || status.State > 9 {
		return printing.Status{}, printing.ErrBackend
	}
	return status, nil
}
func (c *Client) Status(ctx context.Context, id int) (printing.Status, error) {
	path := "/jobs/" + strconv.Itoa(id)
	request := c.jobRequest(goipp.OpGetJobAttributes, path)
	requestedJobAttributes(request)
	response, err := c.perform(ctx, path, request, nil, 0)
	if err != nil {
		return printing.Status{}, err
	}
	status, err := parseJob(response.Job)
	if status.ID != id {
		return printing.Status{}, printing.ErrBackend
	}
	return status, err
}
func (c *Client) Find(ctx context.Context, printer, name string) ([]printing.Status, error) {
	path := "/printers/" + url.PathEscape(printer)
	request := c.jobRequest(goipp.OpGetJobs, path)
	request.Operation.Add(goipp.MakeAttribute("which-jobs", goipp.TagKeyword, goipp.String("all")))
	request.Operation.Add(goipp.MakeAttribute("my-jobs", goipp.TagBoolean, goipp.Boolean(true)))
	requestedJobAttributes(request)
	response, err := c.perform(ctx, path, request, nil, 0)
	if errors.Is(err, printing.ErrNotFound) {
		return []printing.Status{}, nil
	}
	if err != nil {
		return nil, err
	}
	jobs := []printing.Status{}
	for _, group := range response.Groups {
		if group.Tag != goipp.TagJobGroup || firstString(group.Attrs, "job-name") != name {
			continue
		}
		status, err := parseJob(group.Attrs)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, status)
	}
	return jobs, nil
}
func integer(attrs goipp.Attributes, name string, fallback int) int {
	for _, attr := range attrs {
		if attr.Name == name && len(attr.Values) == 1 {
			if value, ok := attr.Values[0].V.(goipp.Integer); ok {
				return int(value)
			}
		}
	}
	return fallback
}
