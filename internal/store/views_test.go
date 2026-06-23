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
