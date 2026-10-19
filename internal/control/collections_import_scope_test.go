package control

import (
	"strings"
	"testing"

	"github.com/Veyal/interseptor/internal/collimport/postman"
	"github.com/Veyal/interseptor/internal/scope"
	"github.com/Veyal/interseptor/internal/store"
)

// The import report states the target host's real scope state. It used to say
// "sends are blocked until the host is in scope" even when the host was in
// scope, and when interactive sends only warn.
func TestImportTargetHostMessageFollowsScopeState(t *testing.T) {
	sc := scope.New()
	sc.SetRules([]store.ScopeRule{{Enabled: true, Action: "include", Host: "in.example.com"}})
	rep := postman.Report{Entries: []postman.Entry{
		{Level: postman.NeedsReview, Feature: "target-host", Message: "3 request(s) target in.example.com", Suggestion: "Check the host is in the engagement scope"},
		{Level: postman.NeedsReview, Feature: "target-host", Message: "1 request(s) target out.example.com", Suggestion: "Check the host is in the engagement scope"},
		{Level: postman.NeedsReview, Feature: "binary-body", Message: "untouched"},
	}}
	annotateTargetHosts(&rep, sc)
	in, out := rep.Entries[0], rep.Entries[1]
	if !strings.Contains(in.Message, "in scope") || strings.Contains(in.Message, "blocked") || in.Level != postman.Converted || in.Suggestion != "" {
		t.Errorf("in-scope host entry = %+v", in)
	}
	if !strings.Contains(out.Message, "not in scope") || !strings.Contains(out.Message, "interactive sends warn") || !strings.Contains(out.Message, "runs") || out.Level != postman.NeedsReview || out.Suggestion == "" {
		t.Errorf("out-of-scope host entry = %+v", out)
	}
	if rep.Entries[2].Message != "untouched" {
		t.Errorf("other entries must not change: %+v", rep.Entries[2])
	}
}
