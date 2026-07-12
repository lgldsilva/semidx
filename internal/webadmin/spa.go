package webadmin

import (
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/lgldsilva/semidx/internal/webui"
)

// spaFileServer serves the embedded React SPA under /admin/, falling back to
// index.html for client-side routes. API and legacy form auth paths are
// registered separately on the mux and take precedence.
func spaFileServer() http.Handler {
	sub, err := fs.Sub(webui.Dist, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "admin SPA embed missing", http.StatusInternalServerError)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Path is /admin, /admin/, /admin/search, /admin/assets/...
		rel := strings.TrimPrefix(r.URL.Path, "/admin")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			serveSPAIndex(w, r, sub)
			return
		}
		// Never serve SPA for API prefixes (belt and suspenders if mis-routed).
		if strings.HasPrefix(rel, "api/") {
			http.NotFound(w, r)
			return
		}
		if tryServeSPAAsset(w, r, sub, rel) {
			return
		}
		serveSPAIndex(w, r, sub)
	})
}

func tryServeSPAAsset(w http.ResponseWriter, r *http.Request, sub fs.FS, rel string) bool {
	f, err := sub.Open(rel)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, statErr := f.Stat()
	if statErr != nil || st.IsDir() {
		return false
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		return false
	}
	http.ServeContent(w, r, path.Base(rel), st.ModTime(), rs)
	return true
}

func serveSPAIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	f, err := sub.Open("index.html")
	if err != nil {
		http.Error(w, "admin SPA index missing — run npm run build in web/", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "admin SPA index stat failed", http.StatusInternalServerError)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "admin SPA index not seekable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", st.ModTime(), rs)
}
