package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageReadsTheProjectsDevcontainer(t *testing.T) {
	for _, test := range []struct {
		name, file, content, want, refusal string
	}{
		{"an image", ".devcontainer/devcontainer.json", `{"image": "mcr.microsoft.com/devcontainers/go:1.25"}`, "mcr.microsoft.com/devcontainers/go:1.25", ""},
		{"comments and trailing commas", ".devcontainer/devcontainer.json", "{\n  // the toolchain\n  \"name\": \"a // b\", /* why */\n  \"image\": \"node:22\",\n}\n", "node:22", ""},
		{"the file at the root", ".devcontainer.json", `{"image": "python:3.13@sha256:abc"}`, "python:3.13@sha256:abc", ""},
		{"a Dockerfile build", ".devcontainer/devcontainer.json", `{"build": {"dockerfile": "Dockerfile"}}`, "", "builds its image"},
		{"a compose file", ".devcontainer/devcontainer.json", `{"dockerComposeFile": "compose.yml"}`, "", "builds its image"},
		{"an image that reads as a flag", ".devcontainer/devcontainer.json", `{"image": "--privileged"}`, "", "not one image reference"},
		{"an image with a space", ".devcontainer/devcontainer.json", `{"image": "node:22 --privileged"}`, "", "not one image reference"},
		{"no image", ".devcontainer/devcontainer.json", `{"name": "x"}`, "", "names no image"},
		{"no devcontainer", "", "", "", "declares no devcontainer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			checkout := t.TempDir()
			if test.file != "" {
				path := filepath.Join(checkout, filepath.FromSlash(test.file))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(test.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			image, err := Image(checkout)

			// Assert
			if test.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), test.refusal) {
					t.Fatalf("Image = %q, %v, want a refusal saying %q", image, err, test.refusal)
				}
				return
			}
			if err != nil || image != test.want {
				t.Fatalf("Image = %q, %v, want %q", image, err, test.want)
			}
		})
	}
}
