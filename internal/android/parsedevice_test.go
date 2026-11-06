package android

import (
	"strings"
	"testing"
)

// The parser must handle both of adb's shapes: the normal tab-separated row and
// the space-separated fallback used by serials that contain spaces. A line that
// ends at the state token (no trailing "product:/model:/transport_id:" fields)
// is valid and must not be dropped, and multi-word states must be kept whole.

func TestParseDeviceLineSpaceSeparatedEndingAtState(t *testing.T) {
	for _, line := range []string{
		"192.168.1.5:5555 device",
		"(no serial number) device",
	} {
		d, ok := parseDeviceLine(line)
		if !ok {
			t.Fatalf("parseDeviceLine(%q) dropped a valid space-separated row", line)
		}
		if d.State != "device" {
			t.Fatalf("parseDeviceLine(%q) state = %q, want device", line, d.State)
		}
	}
	d, ok := parseDeviceLine("192.168.1.5:5555 device")
	if !ok || d.Serial != "192.168.1.5:5555" {
		t.Fatalf("parseDeviceLine serial = %q ok=%v, want 192.168.1.5:5555", d.Serial, ok)
	}

	// A serial with a space, followed by trailing fields, keeps working.
	d, ok = parseDeviceLine("(no serial number)   device product:a54x model:SM_A546E transport_id:3")
	if !ok || d.Serial != "(no serial number)" || d.State != "device" || d.Model != "SM_A546E" || d.TransportID != 3 {
		t.Fatalf("space-containing serial parsed as %#v ok=%v", d, ok)
	}
}

func TestParseDeviceLineKeepsMultiWordState(t *testing.T) {
	// udev permission problems are reported by adb as "no permissions (...)",
	// which must not be truncated to "no".
	line := "????????????\tno permissions (user in plugdev group; are your udev rules wrong?)"
	d, ok := parseDeviceLine(line)
	if !ok {
		t.Fatalf("parseDeviceLine(%q) dropped the row", line)
	}
	if d.State != "no permissions" {
		t.Fatalf("state = %q, want %q", d.State, "no permissions")
	}
	if d.Serial != "????????????" {
		t.Fatalf("serial = %q", d.Serial)
	}

	// Space-separated form of the same state.
	d, ok = parseDeviceLine("ABC123 no permissions (missing udev rules)")
	if !ok || d.State != "no permissions" || d.Serial != "ABC123" {
		t.Fatalf("space-separated no-permissions row parsed as %#v ok=%v", d, ok)
	}
}

// A serial that merely contains a state-like word must not be split at it.
func TestParseDeviceLineDoesNotSplitInsideSerial(t *testing.T) {
	d, ok := parseDeviceLine("device-farm-01\tdevice")
	if !ok || d.Serial != "device-farm-01" || d.State != "device" {
		t.Fatalf("parsed as %#v ok=%v", d, ok)
	}
	if got := parseDevices("List of devices attached\n192.168.1.5:5555 device\n"); len(got) != 1 || !strings.EqualFold(got[0].State, "device") {
		t.Fatalf("parseDevices dropped the space-separated row: %#v", got)
	}
}
