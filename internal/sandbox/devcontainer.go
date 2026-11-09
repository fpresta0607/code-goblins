package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// devcontainerFiles are where a project declares its devcontainer, in the
// order the Development Containers specification looks.
var devcontainerFiles = []string{filepath.Join(".devcontainer", "devcontainer.json"), ".devcontainer.json"}

// imagePattern is an image reference docker run reads as one image: a name
// with an optional registry, tag and digest, which never starts with a dash
// and holds no space, so it can never be read as one of run's own flags.
var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// Image is the image the project in checkout declares in its devcontainer,
// which a sandbox task's container runs. A devcontainer that builds its
// image, from a Dockerfile or a compose file, is refused: building one would
// be a subsystem of its own, so the project names a built image instead. A
// project that declares none is refused with where to declare one.
func Image(checkout string) (string, error) {
	for _, name := range devcontainerFiles {
		data, err := os.ReadFile(filepath.Join(checkout, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("sandbox: read the project's %s: %w", filepath.ToSlash(name), err)
		}
		var config struct {
			Image       string          `json:"image"`
			Build       json.RawMessage `json:"build"`
			DockerFile  string          `json:"dockerFile"`
			ComposeFile json.RawMessage `json:"dockerComposeFile"`
		}
		if err := json.Unmarshal(jsonOfJSONC(data), &config); err != nil {
			return "", fmt.Errorf("sandbox: the project's %s is not valid JSON: %w", filepath.ToSlash(name), err)
		}
		switch {
		case config.Image != "" && !imagePattern.MatchString(config.Image):
			return "", fmt.Errorf("sandbox: the project's %s names the image %q, which is not one image reference", filepath.ToSlash(name), config.Image)
		case config.Image != "":
			return config.Image, nil
		case len(config.Build) > 0 || config.DockerFile != "" || len(config.ComposeFile) > 0:
			return "", fmt.Errorf("sandbox: the project's %s builds its image, from a Dockerfile or a compose file, which cfo sandbox run does not build: name a built image in its image field", filepath.ToSlash(name))
		}
		return "", fmt.Errorf("sandbox: the project's %s names no image", filepath.ToSlash(name))
	}
	return "", errors.New("sandbox: the project declares no devcontainer, so its container has no image: add .devcontainer/devcontainer.json with an image field to its default branch")
}

// jsonOfJSONC is the JSON of a devcontainer.json, which may hold comments
// and trailing commas: the comments are taken out, then the commas, both
// outside strings.
func jsonOfJSONC(data []byte) []byte {
	withoutComments := outsideStrings(string(data), func(text string, index int, out *strings.Builder) int {
		switch {
		case strings.HasPrefix(text[index:], "//"):
			end := strings.IndexByte(text[index:], '\n')
			if end < 0 {
				return len(text)
			}
			return index + end
		case strings.HasPrefix(text[index:], "/*"):
			end := strings.Index(text[index+2:], "*/")
			if end < 0 {
				return len(text)
			}
			return index + end + 4
		}
		out.WriteByte(text[index])
		return index + 1
	})
	return []byte(outsideStrings(withoutComments, func(text string, index int, out *strings.Builder) int {
		if rest := strings.TrimLeft(text[index+1:], " \t\r\n"); text[index] != ',' || !strings.HasPrefix(rest, "}") && !strings.HasPrefix(rest, "]") {
			out.WriteByte(text[index])
		}
		return index + 1
	}))
}

// outsideStrings copies text, each JSON string as it is and everything else
// through step, which writes what it keeps of the text at index and returns
// where the copy goes on.
func outsideStrings(text string, step func(text string, index int, out *strings.Builder) int) string {
	var out strings.Builder
	for index := 0; index < len(text); {
		if text[index] != '"' {
			index = step(text, index, &out)
			continue
		}
		end := index + 1
		for end < len(text) && text[end] != '"' {
			if text[end] == '\\' {
				end++
			}
			end++
		}
		end = min(end+1, len(text))
		out.WriteString(text[index:end])
		index = end
	}
	return out.String()
}
