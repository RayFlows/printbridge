package cups

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Read sysfs only: opening a USB backend can wake a printer or contend with CUPS.
func usbState(root, uri string) string {
	u, err := url.Parse(uri)
	if root == "" || err != nil || u.Scheme != "usb" || u.Host == "" || u.User != nil {
		return "unknown"
	}
	serial := u.Query().Get("serial")
	if serial == "" {
		return "unknown"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "unknown"
	}
	uncertain := false
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name())
		// Interface directories do not have device identity attributes.
		if strings.Contains(entry.Name(), ":") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "serial"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			uncertain = true
			continue
		}
		if strings.TrimSpace(string(data)) != serial {
			continue
		}
		maker, err := os.ReadFile(filepath.Join(dir, "manufacturer"))
		if err != nil {
			uncertain = true
			continue
		}
		if strings.EqualFold(strings.TrimSpace(string(maker)), u.Host) {
			return "connected"
		}
	}
	if uncertain {
		return "unknown"
	}
	return "disconnected"
}
