// Package server is the live board (`prstk board --serve`).
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"prstk/internal/plan"
	"prstk/internal/render"
)

type Server struct {
	Home string
	Now  func() time.Time
	Poll time.Duration

	mu   sync.Mutex
	subs map[chan string]struct{}
}

func New(home string) *Server {
	return &Server{Home: home, Now: time.Now, Poll: time.Second, subs: map[chan string]struct{}{}}
}

// Listen binds the port, failing clearly if it's taken.
func Listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("port %d is unavailable (%v); is another board running? try --port", port, err)
	}
	return ln, nil
}

// Serve runs the HTTP server and the change watcher until ctx is done.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go s.watch(ctx)
	srv := &http.Server{Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	err := srv.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page(func(r *http.Request) render.PageOpts {
		return render.PageOpts{Title: "prstk boards", API: "/api/initiatives", Events: "/events", IndexJS: true}
	}))
	mux.HandleFunc("GET /i/{repo}/{name}/{$}", s.initiativePage(func(in *plan.Initiative, _ *plan.Spec, k string) render.PageOpts {
		return render.PageOpts{Title: in.Name + " · prstk", API: "/api/i/" + k, Events: "/events", Key: k, Base: "/i/" + k + "/"}
	}, false))
	mux.HandleFunc("GET /i/{repo}/{name}/pr/{id}/{$}", s.initiativePage(func(in *plan.Initiative, p *plan.Spec, k string) render.PageOpts {
		return render.PageOpts{Title: p.ID + " " + p.Title + " · prstk", API: "/api/i/" + k + "/pr/" + url.PathEscape(p.ID),
			Events: "/events", View: render.ViewPR, Key: k, Base: "/i/" + k + "/"}
	}, true))
	mux.HandleFunc("GET /i/{repo}/{name}/log/{$}", s.initiativePage(func(in *plan.Initiative, _ *plan.Spec, k string) render.PageOpts {
		return render.PageOpts{Title: "log · " + in.Name + " · prstk", API: "/api/i/" + k + "/log",
			Events: "/events", View: render.ViewLog, Key: k, Base: "/i/" + k + "/"}
	}, false))
	mux.HandleFunc("GET /i/{repo}/{name}", s.slashRedirect(false))
	mux.HandleFunc("GET /i/{repo}/{name}/pr/{id}", s.slashRedirect(true))
	mux.HandleFunc("GET /i/{repo}/{name}/log", s.slashRedirect(false))
	mux.HandleFunc("GET /api/initiatives", s.apiIndex)
	mux.HandleFunc("GET /api/i/{repo}/{name}", s.apiBoard)
	mux.HandleFunc("GET /api/i/{repo}/{name}/pr/{id}", s.apiPR)
	mux.HandleFunc("GET /api/i/{repo}/{name}/log", s.apiLog)
	s.registerArchive(mux)
	mux.HandleFunc("GET /events", s.events)
	return mux
}

func (s *Server) page(opts func(*http.Request) render.PageOpts) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writePage(w, opts(r))
	}
}

func writePage(w http.ResponseWriter, o render.PageOpts) {
	b, err := render.BoardPage(o)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(b)
}

// lookup resolves {repo}/{name} (and {id} when withID) against what is on
// disk. repo and name must pass safeName and load as an initiative; id must
// equal the id of a PR loaded from that initiative. On failure it writes a
// 404 and returns ok=false. Nothing from the URL reaches the filesystem
// except the safeName-checked repo/name; files are then read only via paths
// of the loaded initiative and its PRs.
func (s *Server) lookup(w http.ResponseWriter, r *http.Request, withID bool) (in *plan.Initiative, p *plan.Spec, key string, ok bool) {
	repo, name := r.PathValue("repo"), r.PathValue("name")
	if !safeName(repo) || !safeName(name) {
		http.NotFound(w, r)
		return nil, nil, "", false
	}
	in, err := plan.LoadInitiative(filepath.Join(s.Home, repo, name))
	if err != nil {
		http.Error(w, "initiative not found (torn down?) — back to the index at /", http.StatusNotFound)
		return nil, nil, "", false
	}
	if withID {
		if p = in.PR(r.PathValue("id")); p == nil {
			http.Error(w, "no such PR in this initiative — back to the board at /i/"+repo+"/"+name+"/", http.StatusNotFound)
			return nil, nil, "", false
		}
	}
	return in, p, repo + "/" + name, true
}

func (s *Server) initiativePage(opts func(*plan.Initiative, *plan.Spec, string) render.PageOpts, withID bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if in, p, k, ok := s.lookup(w, r, withID); ok {
			writePage(w, opts(in, p, k))
		}
	}
}

// slashRedirect sends a validated path to its trailing-slash form.
func (s *Server) slashRedirect(withID bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, _, ok := s.lookup(w, r, withID); ok {
			http.Redirect(w, r, r.URL.EscapedPath()+"/", http.StatusMovedPermanently)
		}
	}
}

func safeName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.HasPrefix(n, "_") && !strings.HasPrefix(n, ".") && !strings.ContainsAny(n, `/\`)
}

// initiatives lists active initiative dirs as "<repoKey>/<name>".
func (s *Server) initiatives() []string {
	var out []string
	repos, _ := os.ReadDir(s.Home)
	for _, r := range repos {
		if !r.IsDir() || !safeName(r.Name()) {
			continue
		}
		names, _ := plan.ListInitiatives(filepath.Join(s.Home, r.Name()))
		for _, n := range names {
			out = append(out, r.Name()+"/"+n)
		}
	}
	sort.Strings(out)
	return out
}

type indexEntry struct {
	Key        string `json:"key"`
	Initiative string `json:"initiative"`
	Repo       string `json:"repo"`
	Dir        string `json:"dir"`
	PRs        int    `json:"prs"`
	Slots      int    `json:"slots"`
	MaxWorkers int    `json:"max_workers"`
}

func (s *Server) apiIndex(w http.ResponseWriter, r *http.Request) {
	list := []indexEntry{}
	for _, k := range s.initiatives() {
		in, err := plan.LoadInitiative(filepath.Join(s.Home, k))
		if err != nil {
			continue
		}
		list = append(list, indexEntry{Key: k, Initiative: in.Meta.Initiative, Repo: in.Meta.Repo, Dir: in.Name,
			PRs: len(in.PRs), Slots: in.SlotsUsed(), MaxWorkers: in.MaxWorkers()})
	}
	writeJSON(w, list)
}

func (s *Server) apiBoard(w http.ResponseWriter, r *http.Request) {
	if in, _, _, ok := s.lookup(w, r, false); ok {
		writeJSON(w, render.BuildBoard(in, s.Now()))
	}
}

func (s *Server) apiPR(w http.ResponseWriter, r *http.Request) {
	in, p, _, ok := s.lookup(w, r, true)
	if !ok {
		return
	}
	d, ok := render.BuildPRDetail(in, p.ID, s.Now())
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, d)
}

func (s *Server) apiLog(w http.ResponseWriter, r *http.Request) {
	if in, _, _, ok := s.lookup(w, r, false); ok {
		writeJSON(w, render.InitiativeLog(in))
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

// ---- SSE ----

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := make(chan string, 16)
	s.mu.Lock()
	if s.subs == nil {
		s.subs = map[chan string]struct{}{}
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case k := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", k)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) broadcast(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- key:
		default: // slow client; it'll catch up on the next change
		}
	}
}

// ---- change detection: poll mtimes (no fsnotify; editors save via rename) ----

func (s *Server) signature(key string) string {
	dir := filepath.Join(s.Home, key)
	var b strings.Builder
	add := func(p string) {
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", p, fi.ModTime().UnixNano(), fi.Size())
		}
	}
	add(filepath.Join(dir, "PLAN.md"))
	add(filepath.Join(dir, "LOG.md"))
	add(filepath.Join(filepath.Dir(dir), "config.yaml"))
	prs, _ := os.ReadDir(filepath.Join(dir, "prs"))
	for _, p := range prs {
		add(filepath.Join(dir, "prs", p.Name(), "spec.md"))
		add(filepath.Join(dir, "prs", p.Name(), "notes.md"))
	}
	return b.String()
}

func (s *Server) watch(ctx context.Context) {
	prev := map[string]string{}
	prevList := ""
	t := time.NewTicker(s.Poll)
	defer t.Stop()
	first := true
	for {
		list := s.initiatives()
		cur := map[string]string{}
		for _, k := range list {
			cur[k] = s.signature(k)
			if !first && prev[k] != cur[k] {
				s.broadcast(k)
			}
		}
		joined := strings.Join(list, ",")
		if !first && joined != prevList {
			s.broadcast("*")
		}
		prev, prevList, first = cur, joined, false
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
