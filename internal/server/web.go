package server

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// webdist holds the built receiver page (web/, built by `npm run build`).
//
//go:embed all:webdist
var webdist embed.FS

// WebHandler serves the receiver page under /r/ and the signaling socket
// at /ws.
func (s *Server) WebHandler() http.Handler {
	files, _ := fs.Sub(webdist, "webdist")
	static := http.FileServer(http.FS(files))
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.ServeLinkWS)
	mux.HandleFunc("/r/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/r/")
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		// Downloads (/r/dl/<id>) only exist inside the page's service worker.
		// A browser retrying an interrupted one asks the network instead; it
		// must fail, or it would save whatever came back as the file.
		if strings.HasPrefix(name, "dl/") {
			http.NotFound(w, r)
			return
		}
		// A link's own page (/r/<id>) is the app; anything with a dot is a file.
		if name == "" || !strings.Contains(path.Base(name), ".") {
			h.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' wss: ws:; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
			h.Set("Cache-Control", "no-store")
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			static.ServeHTTP(w, r2)
			return
		}
		if name == "sw.js" {
			h.Set("Cache-Control", "no-cache")
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + name
		static.ServeHTTP(w, r2)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("TropicalPeer rendezvous server\n"))
	})
	return mux
}
