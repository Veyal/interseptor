package vault

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProjectZipRejectsExpandedFileOverLimit(t *testing.T) {
	// Given
	archive := writeValidationZip(t, []validationMember{{name: archiveDBName, data: bytes.Repeat([]byte("x"), 33)}})

	// When
	err := validateProjectZipWithLimits(archive, zipReadLimits{entries: 8, fileBytes: 32, totalBytes: 128})

	// Then
	if err == nil || !strings.Contains(err.Error(), "expanded size limit") {
		t.Fatalf("validate error=%v, want expanded size limit", err)
	}
}

func TestValidateProjectZipAcceptsExactLimits(t *testing.T) {
	// Given
	archive := writeValidationZip(t, []validationMember{{name: archiveDBName, data: bytes.Repeat([]byte("x"), 32)}})

	// When
	err := validateProjectZipWithLimits(archive, zipReadLimits{entries: 1, fileBytes: 32, totalBytes: 32})

	// Then
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateProjectZipRejectsEntryAndCumulativeLimits(t *testing.T) {
	tests := []struct {
		name    string
		members []validationMember
		limits  zipReadLimits
		want    string
	}{
		{name: "entry count", members: []validationMember{{archiveDBName, []byte("db")}, {"extra", []byte("x")}}, limits: zipReadLimits{entries: 1, fileBytes: 32, totalBytes: 64}, want: "entry count limit"},
		{name: "cumulative bytes", members: []validationMember{{archiveDBName, bytes.Repeat([]byte("x"), 24)}, {archiveBodyRoot + "/blob", bytes.Repeat([]byte("x"), 24)}}, limits: zipReadLimits{entries: 8, fileBytes: 32, totalBytes: 40}, want: "cumulative expanded size limit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := writeValidationZip(t, tt.members)

			err := validateProjectZipWithLimits(archive, tt.limits)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validate error=%v, want %s", err, tt.want)
			}
		})
	}
}

type validationMember struct {
	name string
	data []byte
}

func writeValidationZip(t *testing.T, members []validationMember) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "project.zip")
	out, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for _, member := range members {
		w, err := zw.Create(member.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
	return filename
}
