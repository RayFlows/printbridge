package cups

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OpenPrinting/goipp"
	"github.com/RayFlows/printbridge/server/internal/printers"
)

const maxResponseBytes = 4 << 20

type Client struct {
	endpoint string
	http     *http.Client
	usbRoot  string
}

func New(endpoint string, timeout time.Duration) (*Client, error) {
	if err := ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	client := &Client{
		endpoint: endpoint,
		http: &http.Client{
			Timeout: timeout,
			// Never follow a CUPS response to another host or an HTML login page.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	u, _ := url.Parse(endpoint)
	if runtime.GOOS == "linux" && slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname()) {
		client.usbRoot = "/sys/bus/usb/devices"
	}
	return client, nil
}

// WithUSBRoot selects a local sysfs view. An empty path disables presence checks.
func (c *Client) WithUSBRoot(root string) *Client { c.usbRoot = root; return c }

func ValidateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return fmt.Errorf("CUPS URL must be an http(s) origin without credentials, query, or path")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("CUPS URL port must be between 1 and 65535")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("CUPS URL port must not be empty")
	}
	return nil
}

func (c *Client) List(ctx context.Context) ([]printers.Printer, error) {
	request := goipp.NewRequest(goipp.DefaultVersion, goipp.OpCupsGetPrinters, 1)
	request.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	request.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en")))
	var requested goipp.Values
	for _, name := range []string{
		"printer-name", "printer-info", "printer-make-and-model", "printer-location",
		"printer-state", "printer-state-reasons", "printer-state-message", "printer-is-accepting-jobs", "device-uri",
		"media-supported", "sides-supported", "print-color-mode-supported", "copies-supported",
		"media-default", "sides-default", "print-color-mode-default",
		"media-type-supported", "media-source-supported", "media-type-default", "media-source-default",
	} {
		requested.Add(goipp.TagKeyword, goipp.String(name))
	}
	request.Operation.Add(goipp.Attribute{Name: "requested-attributes", Values: requested})
	payload, err := request.EncodeBytes()
	if err != nil {
		return nil, unavailable("encode request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, unavailable("create request")
	}
	req.Header.Set("Content-Type", goipp.ContentType)
	req.Header.Set("Accept", goipp.ContentType)
	response, err := c.http.Do(req)
	if err != nil {
		return nil, unavailable("CUPS connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, unavailable("CUPS HTTP status %d", response.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != goipp.ContentType {
		return nil, unavailable("CUPS returned a non-IPP response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, unavailable("CUPS response could not be read within the size limit")
	}
	var message goipp.Message
	if err := message.DecodeBytes(data); err != nil || message.RequestID != request.RequestID {
		return nil, unavailable("CUPS returned an invalid IPP response")
	}
	// CUPS reports not-found when the scheduler contains no printer queues.
	if goipp.Status(message.Code) == goipp.StatusErrorNotFound {
		return []printers.Printer{}, nil
	}
	if message.Code >= 0x0100 {
		return nil, unavailable("CUPS IPP status 0x%04x", message.Code)
	}
	result := make([]printers.Printer, 0)
	seen := make(map[string]bool)
	for _, group := range message.Groups {
		if group.Tag != goipp.TagPrinterGroup {
			continue
		}
		p, err := parsePrinter(group.Attrs)
		if err != nil {
			return nil, err
		}
		p.DeviceState = usbState(c.usbRoot, firstString(group.Attrs, "device-uri"))
		if seen[p.ID] {
			return nil, unavailable("duplicate printer identifier")
		}
		seen[p.ID] = true
		result = append(result, p)
	}
	slices.SortFunc(result, func(a, b printers.Printer) int { return strings.Compare(a.ID, b.ID) })
	return result, nil
}

func parsePrinter(attrs goipp.Attributes) (printers.Printer, error) {
	p := printers.Printer{
		ID:           firstString(attrs, "printer-name"),
		Name:         firstString(attrs, "printer-info"),
		Model:        firstString(attrs, "printer-make-and-model"),
		Location:     firstString(attrs, "printer-location"),
		State:        "unknown",
		StateReasons: stringsFor(attrs, "printer-state-reasons"),
		StateMessage: firstString(attrs, "printer-state-message"),
		Capabilities: printers.Capabilities{
			Media:              stringsFor(attrs, "media-supported"),
			Sides:              stringsFor(attrs, "sides-supported"),
			ColorModes:         stringsFor(attrs, "print-color-mode-supported"),
			DefaultMedia:       firstString(attrs, "media-default"),
			DefaultSides:       firstString(attrs, "sides-default"),
			DefaultColorMode:   firstString(attrs, "print-color-mode-default"),
			MediaTypes:         stringsFor(attrs, "media-type-supported"),
			MediaSources:       stringsFor(attrs, "media-source-supported"),
			DefaultMediaType:   firstString(attrs, "media-type-default"),
			DefaultMediaSource: firstString(attrs, "media-source-default"),
		},
	}
	if strings.TrimSpace(p.ID) == "" {
		return printers.Printer{}, unavailable("printer response has no identifier")
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	for _, attr := range attrs {
		if len(attr.Values) != 1 {
			continue
		}
		switch attr.Name {
		case "printer-state":
			if state, ok := attr.Values[0].V.(goipp.Integer); ok {
				switch state {
				case 3:
					p.State = "idle"
				case 4:
					p.State = "processing"
				case 5:
					p.State = "stopped"
				}
			}
		case "printer-is-accepting-jobs":
			if value, ok := attr.Values[0].V.(goipp.Boolean); ok {
				accepting := bool(value)
				p.AcceptingJobs = &accepting
			}
		case "copies-supported":
			if value, ok := attr.Values[0].V.(goipp.Range); ok && value.Lower >= 1 && value.Upper >= value.Lower {
				p.Capabilities.Copies = &printers.CopyRange{Min: int(value.Lower), Max: int(value.Upper)}
			}
		}
	}
	return p, nil
}

func stringsFor(attrs goipp.Attributes, name string) []string {
	result := make([]string, 0)
	for _, attr := range attrs {
		if attr.Name != name {
			continue
		}
		for _, value := range attr.Values {
			switch v := value.V.(type) {
			case goipp.String:
				result = append(result, string(v))
			case goipp.TextWithLang:
				result = append(result, v.Text)
			}
		}
	}
	return result
}

func firstString(attrs goipp.Attributes, name string) string {
	values := stringsFor(attrs, name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func unavailable(format string, values ...any) error {
	return fmt.Errorf("%w: %s", printers.ErrUnavailable, fmt.Sprintf(format, values...))
}
