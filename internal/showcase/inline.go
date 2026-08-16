package showcase

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	stylesheetRef = regexp.MustCompile(`(?i)<link\b[^>]*>`)
	scriptSrcRef  = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc="([^"]+)"[^>]*>\s*</script>`)
	imageSrcRef   = regexp.MustCompile(`(?i)<img\b[^>]*\bsrc="([^"]+)"`)
	hrefAttr      = regexp.MustCompile(`(?i)\bhref="([^"]+)"`)
	relAttr       = regexp.MustCompile(`(?i)\brel="([^"]+)"`)
)

// InlineLocalAssets rewrites references to files that sit beside an HTML
// artifact so the result stands alone: stylesheets become <style> blocks,
// scripts become inline <script> blocks, and images become data URIs.
// Remote URLs, fragments, and data URIs are left untouched.
func InlineLocalAssets(source, dir string) string {
	inlined := stylesheetRef.ReplaceAllStringFunc(source, func(tag string) string {
		rel := attrValue(relAttr, tag)
		if !strings.Contains(strings.ToLower(rel), "stylesheet") {
			return tag
		}
		href := attrValue(hrefAttr, tag)
		data, ok := readLocal(dir, href)
		if !ok {
			return tag
		}
		return "<style>\n" + string(data) + "\n</style>"
	})
	inlined = scriptSrcRef.ReplaceAllStringFunc(inlined, func(tag string) string {
		src := scriptSrcRef.FindStringSubmatch(tag)[1]
		data, ok := readLocal(dir, src)
		if !ok {
			return tag
		}
		return "<script>\n" + string(data) + "\n</script>"
	})
	inlined = imageSrcRef.ReplaceAllStringFunc(inlined, func(tag string) string {
		src := imageSrcRef.FindStringSubmatch(tag)[1]
		data, ok := readLocal(dir, src)
		if !ok {
			return tag
		}
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(src)))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return strings.Replace(tag, `"`+src+`"`, `"data:`+mediaType+`;base64,`+base64.StdEncoding.EncodeToString(data)+`"`, 1)
	})
	return inlined
}

// readLocal reads a referenced file only when the reference is a relative
// path that stays inside dir.
func readLocal(dir, ref string) ([]byte, bool) {
	if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "//") ||
		strings.Contains(ref, "://") || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "/") {
		return nil, false
	}
	clean := filepath.Clean(filepath.Join(dir, filepath.FromSlash(ref)))
	root := filepath.Clean(dir)
	if clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator)) {
		return nil, false
	}
	data, err := os.ReadFile(clean)
	if err != nil {
		fmt.Fprintf(os.Stderr, "showcase: export: skipping unreadable asset %s: %v\n", ref, err)
		return nil, false
	}
	return data, true
}

func attrValue(pattern *regexp.Regexp, tag string) string {
	match := pattern.FindStringSubmatch(tag)
	if match == nil {
		return ""
	}
	return match[1]
}
