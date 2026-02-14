package main

import (
	"io/fs"
	"net/http"
	"path"
)

// Serve from a filesystem (can be embedded or OS filesystem)
type spaHandler struct {
	fileSystem fs.FS  // Filesystem to serve from (embedded or os.DirFS)
	indexFile  string // The fallback/default file to serve
}

// Falls back to a supplied index (indexFile) when either condition is true:
// (1) Request (file) path is not found
// (2) Request path is a directory
// Otherwise serves the requested file.
func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean the path and remove leading slash for fs.FS
	cleanPath := path.Clean(r.URL.Path)
	if cleanPath == "/" {
		cleanPath = h.indexFile
	} else {
		cleanPath = cleanPath[1:] // Remove leading slash
	}

	// Try to stat the file
	info, err := fs.Stat(h.fileSystem, cleanPath)
	if err != nil {
		// File not found, serve index
		h.serveIndex(w, r)
		return
	}

	if info.IsDir() {
		// Directory requested, serve index
		h.serveIndex(w, r)
		return
	}

	// Serve the requested file
	http.FileServer(http.FS(h.fileSystem)).ServeHTTP(w, r)
}

func (h *spaHandler) serveIndex(w http.ResponseWriter, _ *http.Request) {
	content, err := fs.ReadFile(h.fileSystem, h.indexFile)
	if err != nil {
		http.Error(w, "Index file not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

// Returns a request handler (http.Handler) that serves a single
// page application from a given filesystem (can be embedded or OS filesystem).
// For embedded: SpaHandler(embeddedFS, "index.html")
// For OS dir: SpaHandler(os.DirFS("public"), "index.html")
func SpaHandler(fileSystem fs.FS, indexFile string) http.Handler {
	return &spaHandler{fileSystem, indexFile}
}
