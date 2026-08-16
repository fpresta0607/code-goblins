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
	scriptSrcRef  = regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)')[^>]*>\s*</script>`)
	imageSrcRef   = regexp.MustCompile(`(?i)(<img\b[^>]*\ssrc\s*=\s*)(?:"([^"]*)"|'([^']*)')([^>]*>)`)
	hrefAttr      = regexp.MustCompile(`(?i)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	relAttr       = regexp.MustCompile(`(?i)\brel\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	cssURLRef     = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^"')]*))\s*\)`)
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
		data, clean, ok := readLocal(dir, dir, href)
		if !ok {
			return tag
		}
		return "<style>\n" + inlineCSSURLs(string(data), filepath.Dir(clean), dir) + "\n</style>"
	})
	inlined = scriptSrcRef.ReplaceAllStringFunc(inlined, func(tag string) string {
		src := attrValue(scriptSrcRef, tag)
		data, _, ok := readLocal(dir, dir, src)
		if !ok {
			return tag
		}
		return "<script>\n" + string(data) + "\n</script>"
	})
	inlined = imageSrcRef.ReplaceAllStringFunc(inlined, func(tag string) string {
		match := imageSrcRef.FindStringSubmatch(tag)
		if match == nil {
			return tag
		}
		src := match[2]
		if src == "" {
			src = match[3]
		}
		data, _, ok := readLocal(dir, dir, src)
		if !ok {
			return tag
		}
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(src)))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return match[1] + `"data:` + mediaType + `;base64,` + base64.StdEncoding.EncodeToString(data) + `"` + match[4]
	})
	return inlined
}

// inlineCSSURLs rewrites url(...) references inside an inlined stylesheet as
// data URIs, resolving them relative to the stylesheet's own directory while
// confining them to root.
func inlineCSSURLs(css, cssDir, root string) string {
	return cssURLRef.ReplaceAllStringFunc(css, func(m string) string {
		match := cssURLRef.FindStringSubmatch(m)
		ref := ""
		for _, v := range match[1:] {
			if v != "" {
				ref = v
				break
			}
		}
		data, _, ok := readLocal(cssDir, root, ref)
		if !ok {
			return m
		}
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(ref)))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return `url("data:` + mediaType + `;base64,` + base64.StdEncoding.EncodeToString(data) + `")`
	})
}

// readLocal reads a referenced file resolved against dir, only when the
// reference is a relative path that stays inside root.
func readLocal(dir, root, ref string) ([]byte, string, bool) {
	if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "//") ||
		strings.Contains(ref, "://") || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "/") {
		return nil, "", false
	}
	clean := filepath.Clean(filepath.Join(dir, filepath.FromSlash(ref)))
	rootClean := filepath.Clean(root)
	if clean != rootClean && !strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return nil, "", false
	}
	data, err := os.ReadFile(clean)
	if err != nil {
		fmt.Fprintf(os.Stderr, "showcase: export: skipping unreadable asset %s: %v\n", ref, err)
		return nil, "", false
	}
	return data, clean, true
}

func attrValue(pattern *regexp.Regexp, tag string) string {
	match := pattern.FindStringSubmatch(tag)
	if match == nil {
		return ""
	}
	if match[1] != "" {
		return match[1]
	}
	return match[2]
}
