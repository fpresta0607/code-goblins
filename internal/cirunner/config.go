package cirunner

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Config is the service's settings, which its install writes to app.json in
// the service's folder, beside the App's key in app-key.pem.
type Config struct {
	AppID          int64  `json:"app_id"`
	InstallationID int64  `json:"installation_id"`
	Owner          string `json:"owner"`
	Repo           string `json:"repo"`
	// Allowed is the SID of the fleet's Windows user, which may open the
	// service's pipe.
	Allowed string `json:"allowed_sid"`
}

// gitHubName is what an owner's or a repository's name may hold, so a name
// is one segment of every path the service calls.
var gitHubName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ReadConfig reads the service's settings and the App's key from dir. A
// setting missing, unknown or out of shape is an error, as is a key that is
// not an RSA private key.
func ReadConfig(dir string) (Config, *rsa.PrivateKey, error) {
	data, err := fsx.ReadFile(filepath.Join(dir, "app.json"))
	if err != nil {
		return Config{}, nil, fmt.Errorf("cirunner: read the service's settings: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, nil, fmt.Errorf("cirunner: the service's settings in app.json: %w", err)
	}
	switch {
	case config.AppID <= 0:
		return Config{}, nil, errors.New("cirunner: app.json names no App id")
	case config.InstallationID <= 0:
		return Config{}, nil, errors.New("cirunner: app.json names no installation id")
	case !gitHubName.MatchString(config.Owner) || !gitHubName.MatchString(config.Repo):
		return Config{}, nil, fmt.Errorf("cirunner: app.json's repository %q/%q is not a GitHub owner and repository", config.Owner, config.Repo)
	case !strings.HasPrefix(config.Allowed, "S-1-"):
		return Config{}, nil, fmt.Errorf("cirunner: app.json's allowed user %q is not a SID", config.Allowed)
	}
	key, err := readKey(filepath.Join(dir, "app-key.pem"))
	if err != nil {
		return Config{}, nil, err
	}
	return config, key, nil
}

// readKey reads the App's private key, in the PKCS #1 form GitHub gives it
// or in PKCS #8.
func readKey(path string) (*rsa.PrivateKey, error) {
	data, err := fsx.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cirunner: read the App's key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("cirunner: the App's key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cirunner: the App's key cannot be read: %w", err)
	}
	key, isRSA := parsed.(*rsa.PrivateKey)
	if !isRSA {
		return nil, fmt.Errorf("cirunner: the App's key is a %T, not an RSA key", parsed)
	}
	return key, nil
}
