package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const defaultServeAddr = "127.0.0.1:4000"

// devServer serves the built site under the base path, as GitHub Pages does,
// and rebuilds when a source file changes. Requests check the sources at most
// once per debounce interval.
type devServer struct {
	root     string
	base     string
	tmp      string
	build    func(root, out string) error
	debounce time.Duration
	now      func() time.Time
	logf     func(format string, args ...any)

	mu       sync.Mutex
	dir      string // current build
	err      error  // last build error
	stamp    string // source fingerprint of the last build
	built    bool
	lastScan time.Time
}

func newDevServer(root, base, tmp string, build func(root, out string) error) *devServer {
	return &devServer{root: root, base: base, tmp: tmp, build: build, debounce: 300 * time.Millisecond, now: time.Now, logf: func(string, ...any) {}}
}

// current returns the directory to serve, rebuilding first when the sources
// changed since the last build.
func (s *devServer) current() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.built && now.Sub(s.lastScan) < s.debounce {
		return s.dir, s.err
	}
	s.lastScan = now
	stamp := sourceFingerprint(s.root)
	if s.built && stamp == s.stamp {
		return s.dir, s.err
	}
	s.stamp, s.built = stamp, true
	started := time.Now()
	dir, err := os.MkdirTemp(s.tmp, "build-")
	if err == nil {
		err = s.build(s.root, dir)
	}
	if err != nil {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		s.err = err
		s.logf("docscheck: build failed: %v\n", err)
		return s.dir, s.err
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
	s.dir, s.err = dir, nil
	s.logf("docscheck: built in %s\n", time.Since(started).Round(time.Millisecond))
	return s.dir, nil
}

func (s *devServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/" || r.URL.Path == s.base {
		http.Redirect(w, r, s.base+"/", http.StatusFound)
		return
	}
	dir, err := s.current()
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "docscheck build failed\n\n%v\n\nFix the source and reload; the site rebuilds automatically.\n", err)
		return
	}
	rel, ok := strings.CutPrefix(r.URL.Path, s.base+"/")
	if !ok {
		s.notFound(w, r, dir)
		return
	}
	rel = path.Clean("/" + rel)
	file := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Stat(file)
	if err == nil && info.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			target := r.URL.Path + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		file = filepath.Join(file, "index.html")
		info, err = os.Stat(file)
	}
	if err != nil || info.IsDir() {
		s.notFound(w, r, dir)
		return
	}
	f, err := os.Open(file)
	if err != nil {
		s.notFound(w, r, dir)
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, file, info.ModTime(), f)
}

// notFound serves the built 404.html with status 404, as GitHub Pages does.
func (s *devServer) notFound(w http.ResponseWriter, r *http.Request, dir string) {
	w.Header().Set("Cache-Control", "no-store")
	page, err := os.ReadFile(filepath.Join(dir, "404.html"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		_, _ = w.Write(page)
	}
}

// sourceFingerprint summarizes the size and modification time of every file
// the build reads; a changed, added or deleted source changes it. Changes to
// Go code (the MCP and route registries) need a restart.
func sourceFingerprint(root string) string {
	h := sha256.New()
	stat := func(rel string) {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			fmt.Fprintf(h, "%s missing\n", rel)
			return
		}
		fmt.Fprintf(h, "%s %d %d\n", rel, info.Size(), info.ModTime().UnixNano())
	}
	for _, rel := range append(sortedKeys(publicDocs), "docs/FEATURES.md", "CHANGELOG.md", llmsFile, llmsFullFile) {
		stat(rel)
	}
	_ = filepath.WalkDir(filepath.Join(root, "website"), func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if rel, err := filepath.Rel(root, file); err == nil {
			stat(filepath.ToSlash(rel))
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// serve runs the development server until interrupted.
func serve(root, addr string, stdout io.Writer) error {
	if !isLoopback(addr) {
		fmt.Fprintf(os.Stderr, "docscheck: warning: %s is not a loopback address; the preview is reachable from other machines\n", addr)
	}
	tmp, err := os.MkdirTemp("", "docscheck-serve-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	server := newDevServer(root, siteBase, tmp, buildSite)
	server.logf = func(format string, args ...any) { fmt.Fprintf(stdout, format, args...) }
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	_, _ = server.current()
	fmt.Fprintf(stdout, "docscheck: serving http://%s%s/ (Ctrl+C to stop)\n", listener.Addr(), siteBase)
	httpServer := &http.Server{Handler: server, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	if err := httpServer.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
