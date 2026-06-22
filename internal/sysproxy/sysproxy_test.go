package sysproxy

import (
	"reflect"
	"testing"
)

func TestParseServicesSkipsHeaderAndDisabled(t *testing.T) {
	out := "An asterisk (*) denotes that a network service is disabled.\nWi-Fi\n*Thunderbolt Bridge\nUSB 10/100/1000 LAN\n"
	got := parseServices(out)
	want := []string{"Wi-Fi", "USB 10/100/1000 LAN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseServices = %v, want %v", got, want)
	}
}

func TestParseServicesEmpty(t *testing.T) {
	if got := parseServices("An asterisk...\n"); len(got) != 0 {
		t.Fatalf("expected no services, got %v", got)
	}
}
