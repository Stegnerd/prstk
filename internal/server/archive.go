package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/Stegnerd/prstk/internal/render"
)

// Archived initiatives (read-only). Teardown moves a finished initiative to
// <home>/<repoKey>/_archive/<dir>/ (see plan.ArchivePath); older teardowns
// left a single <home>/<repoKey>/_archive/<dir>.md instead.
//
// Security model, same as the active routes: {repo} and {name} must pass
// safeName; a dir archive must be a real directory (not a symlink) that loads
// as an initiative; {id} must equal the id of a loaded PR; {file} must equal
// the name of a regular *.md file found by listing _archive. No URL
// parameter is ever joined into a path unless it passed one of those checks,
// and archived files are only read through the loaded initiative's own paths.

// viewLegacy is PageOpts.View for the single-file legacy archive page.
const viewLegacy = "legacy"

// legacySeg is the fixed path segment that separates legacy files from dir
// archives: /a/{repo}/legacy/{file}. A dir archive can't be called "legacy".
const legacySeg = "legacy"

// archiveEntry is one row of GET /api/archives.
type archiveEntry struct {
	Key        string `json:"key"` // dir: "<repoKey>/<dir>"; legacy: "<repoKey>/legacy/<file>.md"
	Initiative string `json:"initiative"`
	Repo       string `json:"repo"`
	Dir        string `json:"dir"`
	PRs        int    `json:"prs"`
	Legacy     bool   `json:"legacy"`
}

var datePrefixRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(?:-\d{4})?`)

// registerArchive adds the archive routes to mux.
func (s *Server) registerArchive(mux *http.ServeMux) {
	mux.HandleFunc("GET /a/{repo}/{name}/{$}", s.archivePage(func(in *plan.Initiative, _ *plan.Spec, k, base string) render.PageOpts {
		return render.PageOpts{Title: in.Name + " (archived) · prstk", API: "/api/a/" + base, Key: k, Base: "/a/" + base + "/", ReadOnly: true}
	}, false))
	mux.HandleFunc("GET /a/{repo}/{name}/pr/{id}/{$}", s.archivePage(func(in *plan.Initiative, p *plan.Spec, k, base string) render.PageOpts {
		return render.PageOpts{Title: p.ID + " " + p.Title + " (archived) · prstk", API: "/api/a/" + base + "/pr/" + url.PathEscape(p.ID),
			View: render.ViewPR, Key: k, Base: "/a/" + base + "/", ReadOnly: true}
	}, true))
	mux.HandleFunc("GET /a/{repo}/{name}/log/{$}", s.archivePage(func(in *plan.Initiative, _ *plan.Spec, k, base string) render.PageOpts {
		return render.PageOpts{Title: "log · " + in.Name + " (archived) · prstk", API: "/api/a/" + base + "/log",
			View: render.ViewLog, Key: k, Base: "/a/" + base + "/", ReadOnly: true}
	}, false))
	// Slash-less forms redirect after validation, like /i/. A literal
	// "/a/{repo}/{name}/log" route would conflict with the legacy route
	// below, so the single {tail} segment is checked in the handler (a "..."
	// wildcard would make the mux itself redirect before any validation).
	mux.HandleFunc("GET /a/{repo}/{name}", s.archiveSlashRedirect(false))
	mux.HandleFunc("GET /a/{repo}/{name}/pr/{id}", s.archiveSlashRedirect(true))
	mux.HandleFunc("GET /a/{repo}/{name}/{tail}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("tail") != "log" {
			http.NotFound(w, r)
			return
		}
		s.archiveSlashRedirect(false)(w, r)
	})
	mux.HandleFunc("GET /a/{repo}/legacy/{file}", s.legacyPage)
	s.registerEvidence(mux, "/a", s.lookupArchive)

	mux.HandleFunc("GET /api/archives", s.apiArchives)
	mux.HandleFunc("GET /api/a/{repo}/{name}", s.apiArchiveBoard)
	mux.HandleFunc("GET /api/a/{repo}/{name}/pr/{id}", s.apiArchivePR)
	mux.HandleFunc("GET /api/a/{repo}/{name}/log", s.apiArchiveLog)
	// Not under /api/a/{repo}/legacy/: that would conflict with .../{name}/log.
	mux.HandleFunc("GET /api/legacy/{repo}/{file}", s.apiLegacy)
}

// archiveBase is the escaped "<repo>/<name>" used in archive URLs.
func archiveBase(repo, name string) string {
	return url.PathEscape(repo) + "/" + url.PathEscape(name)
}

// lookupArchive is lookup rooted at <home>/<repo>/_archive. On failure it
// writes a 404 and returns ok=false.
func (s *Server) lookupArchive(w http.ResponseWriter, r *http.Request, withID bool) (in *plan.Initiative, p *plan.Spec, key string, ok bool) {
	repo, name := r.PathValue("repo"), r.PathValue("name")
	if !safeName(repo) || !safeName(name) || name == legacySeg {
		http.NotFound(w, r)
		return nil, nil, "", false
	}
	dir := plan.ArchivePath(filepath.Join(s.Home, repo), name)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() { // Lstat: a symlink is not a dir
		http.Error(w, "no such archived initiative — back to the index at /", http.StatusNotFound)
		return nil, nil, "", false
	}
	in, err := plan.LoadInitiative(dir)
	if err != nil {
		http.Error(w, "no such archived initiative — back to the index at /", http.StatusNotFound)
		return nil, nil, "", false
	}
	// LoadInitiative derives these from the path, which here is _archive.
	in.RepoKey = repo
	in.Config, _ = plan.LoadConfig(filepath.Join(s.Home, repo))
	if withID {
		if p = in.PR(r.PathValue("id")); p == nil {
			http.Error(w, "no such PR in this archived initiative — back to its board at /a/"+archiveBase(repo, name)+"/", http.StatusNotFound)
			return nil, nil, "", false
		}
	}
	return in, p, repo + "/" + name, true
}

func (s *Server) archivePage(opts func(in *plan.Initiative, p *plan.Spec, key, base string) render.PageOpts, withID bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if in, p, k, ok := s.lookupArchive(w, r, withID); ok {
			writePage(w, opts(in, p, k, archiveBase(r.PathValue("repo"), r.PathValue("name"))))
		}
	}
}

// archiveSlashRedirect sends a validated path to its trailing-slash form.
func (s *Server) archiveSlashRedirect(withID bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, _, ok := s.lookupArchive(w, r, withID); ok {
			http.Redirect(w, r, r.URL.EscapedPath()+"/", http.StatusMovedPermanently)
		}
	}
}

func (s *Server) apiArchiveBoard(w http.ResponseWriter, r *http.Request) {
	if in, _, _, ok := s.lookupArchive(w, r, false); ok {
		writeJSON(w, render.BuildBoard(in, s.Now()))
	}
}

func (s *Server) apiArchivePR(w http.ResponseWriter, r *http.Request) {
	in, p, _, ok := s.lookupArchive(w, r, true)
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

func (s *Server) apiArchiveLog(w http.ResponseWriter, r *http.Request) {
	if in, _, _, ok := s.lookupArchive(w, r, false); ok {
		writeJSON(w, render.InitiativeLog(in))
	}
}

// ---- legacy single-file archives ----

// legacyDoc is GET /api/legacy/{repo}/{file}.
type legacyDoc struct {
	Key        string `json:"key"`
	Initiative string `json:"initiative"`
	Repo       string `json:"repo"`
	Dir        string `json:"dir"`
	File       string `json:"file"`
	Body       string `json:"body"` // markdown, frontmatter stripped; untrusted
}

// legacyFile returns the path of {file} only if it names a regular *.md file
// that listing the repo's _archive actually finds. The returned name comes
// from the listing, not from the URL.
func (s *Server) legacyFile(w http.ResponseWriter, r *http.Request) (repo, file string, ok bool) {
	repo, want := r.PathValue("repo"), r.PathValue("file")
	if safeName(repo) && safeName(want) && strings.HasSuffix(want, ".md") {
		entries, _ := os.ReadDir(plan.ArchiveRoot(filepath.Join(s.Home, repo)))
		for _, e := range entries {
			if e.Name() == want && e.Type().IsRegular() {
				return repo, e.Name(), true
			}
		}
	}
	http.Error(w, "no such legacy archive — back to the index at /", http.StatusNotFound)
	return "", "", false
}

func (s *Server) loadLegacy(repo, file string) (legacyDoc, error) {
	path := filepath.Join(plan.ArchiveRoot(filepath.Join(s.Home, repo)), file)
	data, err := os.ReadFile(path)
	if err != nil {
		return legacyDoc{}, err
	}
	dir := strings.TrimSuffix(file, ".md")
	d := legacyDoc{Key: repo + "/" + legacySeg + "/" + file, Initiative: strings.TrimPrefix(strings.TrimPrefix(dir, datePrefixRe.FindString(dir)), "-"),
		Repo: repo, Dir: dir, File: file, Body: string(data)}
	// Old teardowns wrote the PLAN.md frontmatter first. Use it for the
	// names and drop it from the rendered body; fall back to the file name.
	if doc, err := plan.ParseDoc(path, data); err == nil {
		var m plan.PlanMeta
		if doc.Decode(&m) == nil {
			if m.Initiative != "" {
				d.Initiative = m.Initiative
			}
			if m.Repo != "" {
				d.Repo = m.Repo
			}
		}
		d.Body = doc.Body
	}
	if d.Initiative == "" {
		d.Initiative = dir
	}
	return d, nil
}

func (s *Server) legacyPage(w http.ResponseWriter, r *http.Request) {
	repo, file, ok := s.legacyFile(w, r)
	if !ok {
		return
	}
	writePage(w, render.PageOpts{
		Title: strings.TrimSuffix(file, ".md") + " (archived) · prstk",
		API:   "/api/legacy/" + url.PathEscape(repo) + "/" + url.PathEscape(file),
		View:  viewLegacy, Key: repo + "/" + legacySeg + "/" + file, ReadOnly: true,
	})
}

func (s *Server) apiLegacy(w http.ResponseWriter, r *http.Request) {
	repo, file, ok := s.legacyFile(w, r)
	if !ok {
		return
	}
	d, err := s.loadLegacy(repo, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, d)
}

// ---- listing ----

func (s *Server) apiArchives(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.archives())
}

// archives lists every dir archive (one holding a loadable PLAN.md) and every
// legacy *.md file under <home>/*/_archive, newest first by the date prefix of
// the directory name.
func (s *Server) archives() []archiveEntry {
	out := []archiveEntry{}
	repos, _ := os.ReadDir(s.Home)
	for _, rd := range repos {
		if !rd.IsDir() || !safeName(rd.Name()) {
			continue
		}
		repo := rd.Name()
		entries, _ := os.ReadDir(plan.ArchiveRoot(filepath.Join(s.Home, repo)))
		for _, e := range entries {
			name := e.Name()
			if !safeName(name) {
				continue
			}
			switch {
			case e.IsDir() && name != legacySeg: // IsDir is false for a symlink
				in, err := plan.LoadInitiative(plan.ArchivePath(filepath.Join(s.Home, repo), name))
				if err != nil {
					continue
				}
				out = append(out, archiveEntry{Key: repo + "/" + name, Initiative: in.Meta.Initiative, Repo: in.Meta.Repo, Dir: name, PRs: len(in.PRs)})
			case e.Type().IsRegular() && strings.HasSuffix(name, ".md"):
				d, err := s.loadLegacy(repo, name)
				if err != nil {
					continue
				}
				out = append(out, archiveEntry{Key: d.Key, Initiative: d.Initiative, Repo: d.Repo, Dir: d.Dir, Legacy: true})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := datePrefixRe.FindString(out[i].Dir), datePrefixRe.FindString(out[j].Dir)
		if pi != pj {
			return pi > pj // newest first; no prefix sorts last
		}
		return out[i].Key < out[j].Key
	})
	return out
}
