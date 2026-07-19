package vault

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Server is the HTTP vault API.
type Server struct {
	Store  *Store
	Auth   *Auth
	Addr   string // listen address (set after Listen)
	ln     net.Listener
	mux    *http.ServeMux
}

// NewServer builds handlers.
func NewServer(st *Store, auth *Auth) *Server {
	s := &Server{Store: st, Auth: auth, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/vault/status", s.require(ScopeRead, s.status))
	s.mux.HandleFunc("GET /api/vault/projects", s.require(ScopeRead, s.listProjects))
	s.mux.HandleFunc("GET /api/vault/projects/{id}", s.require(ScopeRead, s.getProject))
	s.mux.HandleFunc("PUT /api/vault/projects/{id}", s.require(ScopeFull, s.putProject))
	s.mux.HandleFunc("GET /api/vault/projects/{id}/latest", s.require(ScopeRead, s.getLatest))
	s.mux.HandleFunc("GET /api/vault/projects/{id}/revs/{n}", s.require(ScopeRead, s.getRev))
	s.mux.HandleFunc("DELETE /api/vault/projects/{id}/revs/{n}", s.require(ScopeFull, s.deleteRev))
	s.mux.HandleFunc("DELETE /api/vault/projects/{id}", s.require(ScopeFull, s.deleteProject))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

type handlerFunc func(http.ResponseWriter, *http.Request)

func (s *Server) require(min TokenScope, next handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, err := s.Auth.Check(r.Header.Get("Authorization"))
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if min == ScopeFull && scope != ScopeFull {
			http.Error(w, `{"error":"forbidden: full-scope token required"}`, http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	st := s.Store.Status()
	st["addr"] = s.Addr
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []ProjectInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": list})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	revs, err := s.Store.Revisions(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "revisions": revs})
}

func (s *Server) putProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	label := r.URL.Query().Get("label")
	source := r.Header.Get("X-Interseptor-Source-Host")
	if source == "" {
		source = r.RemoteAddr
	}
	info, err := s.Store.Put(id, label, source, io.LimitReader(r.Body, 4<<30))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) getLatest(w http.ResponseWriter, r *http.Request) {
	s.streamRev(w, r.PathValue("id"), 0)
}

func (s *Server) getRev(w http.ResponseWriter, r *http.Request) {
	n, err := ParseRev(r.PathValue("n"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.streamRev(w, r.PathValue("id"), n)
}

func (s *Server) streamRev(w http.ResponseWriter, id string, rev int) {
	rc, info, err := s.Store.OpenRev(id, rev)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-rev-%06d.zip"`, id, info.Rev))
	w.Header().Set("X-Vault-Rev", strconv.Itoa(info.Rev))
	w.Header().Set("X-Vault-Sha256", info.SHA256)
	_, _ = io.Copy(w, rc)
}

func (s *Server) deleteRev(w http.ResponseWriter, r *http.Request) {
	n, err := ParseRev(r.PathValue("n"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteRev(r.PathValue("id"), n); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteProject(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListenAndServe binds addr and serves until ln is closed.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.Addr = ln.Addr().String()
	return http.Serve(ln, s.mux)
}

// Handler exposes the mux for tests.
func (s *Server) Handler() http.Handler { return s.mux }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// NormalizeAddr ensures host:port (default 127.0.0.1:9977).
func NormalizeAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "127.0.0.1:9977"
	}
	if !strings.Contains(addr, ":") {
		return "127.0.0.1:" + addr
	}
	return addr
}
