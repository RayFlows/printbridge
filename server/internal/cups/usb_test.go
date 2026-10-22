package cups

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUSBPresenceIsIndependentOfQueueState(t *testing.T) {
	root := t.TempDir()
	uri := "usb://Synthetic/Example?serial=test-serial&interface=1"
	if got := usbState(root, uri); got != "disconnected" {
		t.Fatal(got)
	}
	dir := filepath.Join(root, "1-2")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"serial": "test-serial\n", "manufacturer": "Synthetic\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := usbState(root, uri); got != "connected" {
		t.Fatal(got)
	}
	for _, item := range []struct{ root, uri string }{
		{"", uri}, {filepath.Join(root, "missing"), uri}, {root, ""},
		{root, "ipp://printer.test/ipp/print"}, {root, "usb://Synthetic/Example"},
	} {
		if got := usbState(item.root, item.uri); got != "unknown" {
			t.Fatal(item, got)
		}
	}
	if got := usbState(root, "usb://Another/Example?serial=test-serial"); got != "disconnected" {
		t.Fatal("manufacturer mismatch", got)
	}
}
