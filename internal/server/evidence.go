package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Stegnerd/prstk/internal/plan"
)

// Evidence files (live and archive):
//
//	GET /{i,a}/{repo}/{name}/pr/{id}/evidence/{phase}/{file}
//	GET /{i,a}/{repo}/{name}/pr/{id}/evidence/script
//
// Security model: {repo}/{name}/{id} resolve through lookup/lookupArchive as
// for every other route. {phase} must be exactly "before" or "after". {file}
// is only a lookup key: it must equal an item's File in that phase of the
// loaded manifest, and the path served is always the spec's
// EvidenceFilePath(item) / EvidenceScriptPath(manifest). The resolved path
// must sit inside the PR's evidence dir after Clean, every directory between
// the two must be a real directory, and the file itself a regular file (no
// symlinks anywhere below evidence/). Every failure is a bare 404.

// lookupFunc is Server.lookup or Server.lookupArchive.
type lookupFunc func(w http.ResponseWriter, r *http.Request, withID bool) (*plan.Initiative, *plan.Spec, string, bool)

// evidenceTypes is the Content-Type allowlist by (lowercased) extension.
var evidenceTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
	".webm": "video/webm",
	".mp4":  "video/mp4",
}

const evidenceCSP = "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'"

func (s *Server) registerEvidence(mux *http.ServeMux, prefix string, look lookupFunc) {
	mux.HandleFunc("GET "+prefix+"/{repo}/{name}/pr/{id}/evidence/{phase}/{file}", evidenceFile(look))
	mux.HandleFunc("GET "+prefix+"/{repo}/{name}/pr/{id}/evidence/script", evidenceScript(look))
}

func evidenceFile(look lookupFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, p, _, ok := look(w, r, true)
		if !ok {
			return
		}
		var phase plan.Phase
		switch r.PathValue("phase") {
		case string(plan.PhaseBefore):
			phase = plan.PhaseBefore
		case string(plan.PhaseAfter):
			phase = plan.PhaseAfter
		default:
			http.NotFound(w, r)
			return
		}
		m, err := plan.LoadEvidence(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		it, ok := m.Lookup(phase, r.PathValue("file"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		ctype, ok := evidenceTypes[strings.ToLower(filepath.Ext(it.File))]
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveEvidence(w, r, p.EvidenceDir(), p.EvidenceFilePath(it), ctype)
	}
}

func evidenceScript(look lookupFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, p, _, ok := look(w, r, true)
		if !ok {
			return
		}
		m, err := plan.LoadEvidence(p)
		if err != nil || m.Script == "" {
			http.NotFound(w, r)
			return
		}
		serveEvidence(w, r, p.EvidenceDir(), p.EvidenceScriptPath(m), "text/plain; charset=utf-8")
	}
}

// serveEvidence serves path (built from the manifest, never from the URL)
// after checking it is a regular file strictly inside root, reached through
// real directories only.
func serveEvidence(w http.ResponseWriter, r *http.Request, root, path, ctype string) {
	root, path = filepath.Clean(root), filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		http.NotFound(w, r)
		return
	}
	// root and each directory below it must be a real directory (Lstat: a
	// symlinked evidence/ or evidence/<phase>/ is refused too).
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		if fi, err := os.Lstat(d); err != nil || !fi.IsDir() {
			http.NotFound(w, r)
			return
		}
		if d == root || d == filepath.Dir(d) {
			break
		}
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	// The file opened must be the one checked (not swapped for a symlink in between).
	if ofi, err := f.Stat(); err != nil || !os.SameFile(fi, ofi) {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", evidenceCSP)
	h.Set("Cache-Control", "no-cache")
	// The name only feeds ServeContent's type sniffing, which the
	// Content-Type above already disables.
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
