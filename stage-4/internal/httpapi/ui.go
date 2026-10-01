package httpapi

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

//go:embed web
var webFS embed.FS

// assets serves the embedded stylesheet and script under /assets/.
func assets() http.Handler {
	sub, err := fs.Sub(webFS, "web/assets")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/assets/", http.FileServerFS(sub))
}

func readPage() []byte {
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		panic(err)
	}
	return page
}

var page = readPage()

// servePage writes the single-page shell; the script renders the screen
// for the current path.
func servePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(page); err != nil {
		log.Printf("write page: %v", err)
	}
}

// wantsHTML reports whether the client asked for a browser page.
func wantsHTML(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// negotiate serves the page to browsers and the JSON handler to everyone
// else, for routes the UI and the API share.
func negotiate(api http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wantsHTML(r) {
			servePage(w, r)
			return
		}
		api(w, r)
	}
}

// registerPages routes the screens that have no JSON counterpart, and the
// assets they load. The exact-path patterns keep unknown paths on the JSON
// 404.
func registerPages(route func(pattern string, handlers map[string]http.HandlerFunc)) {
	for _, path := range []string{"/{$}", "/split", "/signup", "/login"} {
		route(path, map[string]http.HandlerFunc{"GET": servePage})
	}
	route("/assets/{file}", map[string]http.HandlerFunc{"GET": assets().ServeHTTP})
}
