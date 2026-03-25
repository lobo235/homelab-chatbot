// Package frontend serves the embedded HTML/CSS/JS frontend.
package frontend

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

//go:embed help.html
var helpHTML []byte

// HandleIndex serves the main chat UI.
func HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(indexHTML)
}

// HandleHelp serves the help page.
func HandleHelp(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(helpHTML)
}
