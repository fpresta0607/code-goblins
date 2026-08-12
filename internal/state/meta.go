// Package state reads and writes the on-disk task state First Mate defined:
// key=value meta files and append-only status logs under a home's state dir.
package state

import (
	"maps"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ReadMeta parses a state/<id>.meta file: one key=value pair per line, the
// last occurrence of a key wins, and lines without a key before '=' are inert.
// The shape matches upstream First Mate's meta readers (grep "^key=" | tail -1).
func ReadMeta(path string) (map[string]string, error) {
	lines, err := fsx.ReadLines(path)
	if err != nil {
		return nil, err
	}
	kv := make(map[string]string)
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			continue
		}
		kv[key] = value
	}
	return kv, nil
}

// WriteMeta atomically writes kv as sorted key=value lines.
func WriteMeta(path string, kv map[string]string) error {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(kv)) {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(kv[k])
		b.WriteByte('\n')
	}
	return fsx.AtomicWriteFile(path, []byte(b.String()))
}
