package supervisor

import (
	"bytes"
	"crypto/rand"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (h *HTTP) afkReportFile(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/afk/report/")
	if !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) {
		http.NotFound(w, r)
		return
	}
	// The off snapshot can arrive while the switch still writes its report.
	h.Service.afkChange.Lock()
	file, err := os.OpenInRoot(filepath.Join(h.Service.Store.Home.State, afkReturnDir), name)
	h.Service.afkChange.Unlock()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if filepath.Base(name) != "index.html" {
		http.ServeContent(w, r, name, info.ModTime(), file)
		return
	}
	data, err := io.ReadAll(file)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "The AFK report could not be read")
		return
	}
	nonce := rand.Text()
	data = bytes.ReplaceAll(data, []byte("<style>"), []byte(`<style nonce="`+nonce+`">`))
	data = bytes.ReplaceAll(data, []byte("<script>"), []byte(`<script nonce="`+nonce+`">`))
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'nonce-"+nonce+"'; style-src 'nonce-"+nonce+"'; style-src-attr 'unsafe-inline'; img-src 'self'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
