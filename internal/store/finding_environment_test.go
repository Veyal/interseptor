package store

import (
	"errors"
	"testing"
)

func TestFindingEnvironmentRoundTripAndValidation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, env := range []string{"", "development", "testing", "production", "prod", "staging", "local"} {
		id, err := s.CreateFinding(&Finding{Title: "Environment", Environment: env})
		if err != nil {
			t.Fatal(err)
		}
		f, err := s.GetFinding(id)
		if err != nil || f.Environment != env {
			t.Fatalf("create %q: %+v %v", env, f, err)
		}
		if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &env, nil, nil); err != nil {
			t.Fatal(err)
		}
		f, _ = s.GetFinding(id)
		if f.Environment != env {
			t.Fatalf("patch %q became %q", env, f.Environment)
		}
		invalid := "unrecognized"
		if err := s.UpdateFinding(id, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &invalid, nil, nil); !errors.Is(err, ErrInvalidFinding) {
			t.Fatalf("invalid patch: %v", err)
		}
		f, _ = s.GetFinding(id)
		if f.Environment != env {
			t.Fatal("invalid update changed stored environment")
		}
	}
	if _, err := s.CreateFinding(&Finding{Title: "Invalid", Environment: "unknown"}); !errors.Is(err, ErrInvalidFinding) {
		t.Fatalf("invalid create: %v", err)
	}
}
