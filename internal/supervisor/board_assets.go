package supervisor

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// keptAsset is the Cache-Control of a board file named by its content, which
// can never change under that name, so a browser keeps it a year without
// asking again (#1966). It is private: the board reached over remote access is
// his alone, and no cache on the way may hand it to anyone else.
const keptAsset = "private, max-age=31536000, immutable"

// compressedTypes are the board's scripts and styles, sent gzip-compressed to
// a browser that takes it, each with its own type, as a registry that names
// JavaScript text/plain would break a module script.
var compressedTypes = map[string]string{".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8"}

// hashedFiles are the files the board's build named by a hash of their content,
// as its manifest lists them: every script, style and file Vite writes. A
// board built without one, or the placeholder page, names none.
func hashedFiles(assets fs.FS) map[string]bool {
	files := map[string]bool{}
	data, err := fs.ReadFile(assets, ".vite/manifest.json")
	if err != nil {
		return files
	}
	var manifest map[string]struct {
		File   string   `json:"file"`
		CSS    []string `json:"css"`
		Assets []string `json:"assets"`
	}
	if json.Unmarshal(data, &manifest) != nil {
		return files
	}
	for _, chunk := range manifest {
		files[chunk.File] = true
		for _, file := range append(chunk.CSS, chunk.Assets...) {
			files[file] = true
		}
	}
	return files
}

// serveAsset answers a file under /assets/. A file Vite wrote is kept a year,
// and so is one the build copied from public/ under its own name when the
// address carries the digest of its content the build wrote into it (v);
// asked for without it, as the AFK return page and Scrawl ask for the fonts,
// such a file can change under its name and stays no-store.
func (h *HTTP) serveAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if h.hashed[name] || h.isDigestOf(name, r.URL.Query().Get("v")) {
		w.Header().Set("Cache-Control", keptAsset)
	}
	contentType, isCompressed := compressedTypes[path.Ext(name)]
	if !isCompressed {
		http.FileServer(http.FS(h.Assets)).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Add("Vary", "Accept-Encoding")
	if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		http.FileServer(http.FS(h.Assets)).ServeHTTP(w, r)
		return
	}
	body, err := h.gzipped(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}

// isDigestOf reports whether version is the digest of the named file, the
// first 16 hex digits of its SHA-256, as frontend/vite.config.ts writes it.
func (h *HTTP) isDigestOf(name, version string) bool {
	if version == "" {
		return false
	}
	data, err := fs.ReadFile(h.Assets, name)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return version == hex.EncodeToString(sum[:8])
}

// gzipped is a board file compressed once, on the first request for it, and
// kept for every later one.
func (h *HTTP) gzipped(name string) ([]byte, error) {
	h.compressedMu.Lock()
	body, isKept := h.compressed[name]
	h.compressedMu.Unlock()
	if isKept {
		return body, nil
	}
	data, err := fs.ReadFile(h.Assets, name)
	if err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	h.compressedMu.Lock()
	h.compressed[name] = compressed.Bytes()
	h.compressedMu.Unlock()
	return compressed.Bytes(), nil
}

// acceptsGzip reports whether an Accept-Encoding header takes gzip: it names
// gzip with no quality of zero.
func acceptsGzip(header string) bool {
	for _, coding := range strings.Split(header, ",") {
		name, parameters, _ := strings.Cut(coding, ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		key, value, hasQuality := strings.Cut(parameters, "=")
		if !hasQuality || !strings.EqualFold(strings.TrimSpace(key), "q") {
			return true
		}
		quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return err == nil && quality > 0
	}
	return false
}
