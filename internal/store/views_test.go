package store

import "testing"

func TestSavedViewCRUD(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if vs, _ := s.ListViews(); len(vs) != 0 {
		t.Fatalf("expected no views, got %d", len(vs))
	}
	id, err := s.CreateView(&SavedView{Name: "4xx on acme", Data: `{"status":"4","host":"acme"}`})
	if err != nil || id == 0 {
		t.Fatalf("CreateView: id=%d err=%v", id, err)
	}
	vs, _ := s.ListViews()
	if len(vs) != 1 || vs[0].Name != "4xx on acme" || vs[0].Data == "" {
		t.Fatalf("unexpected views: %+v", vs)
	}
	if err := s.DeleteView(id); err != nil {
		t.Fatalf("DeleteView: %v", err)
	}
	if vs, _ := s.ListViews(); len(vs) != 0 {
		t.Fatalf("expected view deleted, got %d", len(vs))
	}
}

// Regression: saving a view under an existing name listed it twice.
func TestSavedViewSaveByExistingNameReplacesIt(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	first, _ := s.CreateView(&SavedView{Name: "errors", Data: `{"status":"4"}`})
	second, err := s.CreateView(&SavedView{Name: "errors", Data: `{"status":"5"}`})
	if err != nil || second != first {
		t.Fatalf("second save: id=%d (first %d) err=%v", second, first, err)
	}
	vs, _ := s.ListViews()
	if len(vs) != 1 || vs[0].Data != `{"status":"5"}` {
		t.Fatalf("want one replaced view, got %+v", vs)
	}
	if _, err := s.CreateView(&SavedView{Name: "other", Data: `{}`}); err != nil {
		t.Fatal(err)
	}
	if vs, _ := s.ListViews(); len(vs) != 2 {
		t.Fatalf("a different name stays separate, got %+v", vs)
	}
}
