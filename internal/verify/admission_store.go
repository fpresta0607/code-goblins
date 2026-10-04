package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func isTestBinary() bool {
	name := strings.ToLower(filepath.Base(os.Args[0]))
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")
}

// AdmissionDir is the machine's common store. Only test binaries use their
// explicitly isolated report store; production report redirects do not move it.
func AdmissionDir() (string, error) {
	if isTestBinary() {
		store, err := StoreDir()
		if err != nil {
			return "", err
		}
		return admissionDir(filepath.Join(store, "slots"), true)
	}
	return admissionDir("", false)
}

func admissionDir(named string, isTest bool) (string, error) {
	if isTest {
		if named == "" {
			return "", errors.New("verify: a test must name its isolated admission store")
		}
		return fsx.AbsClean(named)
	}
	cache, err := admissionCacheDir()
	if err != nil {
		return "", fmt.Errorf("verify: resolve canonical admission store: %w", err)
	}
	canonical := filepath.Join(cache, "cfo", "verify", "slots")
	if named != "" {
		absolute, err := fsx.AbsClean(named)
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(absolute, canonical) {
			return "", errors.New("verify: production admission cannot use a private store")
		}
	}
	return canonical, nil
}

// admissionCapacity has one shared setting. Its absence retains capacity one;
// this delivery rejects any increase pending measured concurrency evidence.
func admissionCapacity(dir string) (int, error) {
	data, err := fsx.ReadFile(filepath.Join(dir, "capacity.json"))
	if errors.Is(err, os.ErrNotExist) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return 0, errors.New("verify: capacity.json must contain only capacity: 1")
	}
	key, err := decoder.Token()
	if err != nil || key != "capacity" {
		return 0, errors.New("verify: capacity.json must contain only capacity: 1")
	}
	var capacity int
	if err := decoder.Decode(&capacity); err != nil {
		return 0, fmt.Errorf("verify: invalid shared capacity: %w", err)
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || capacity != 1 {
		return 0, errors.New("verify: capacity.json must contain only capacity: 1")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return 0, errors.New("verify: capacity.json has trailing data")
	}
	return capacity, nil
}
