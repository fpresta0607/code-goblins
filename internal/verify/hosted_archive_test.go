package verify

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func hostedZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, data := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestHostedArtifactRejectsCorruptAmbiguousAndOversizedProof(t *testing.T) {
	_, fixture := hostedFixture()
	manifest, err := json.Marshal(fixture.Manifests[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		files   map[string][]byte
		isValid bool
	}{
		{"complete", map[string][]byte{"manifest.json": manifest}, true},
		{"path traversal", map[string][]byte{"../manifest.json": manifest}, false},
		{"extra file", map[string][]byte{"manifest.json": manifest, "other.json": manifest}, false},
		{"malformed", map[string][]byte{"manifest.json": []byte("not JSON")}, false},
		{"trailing JSON", map[string][]byte{"manifest.json": append(bytes.Clone(manifest), []byte(`{}`)...)}, false},
		{"duplicate key", map[string][]byte{"manifest.json": []byte(`{"version":1,"version":1}`)}, false},
		{"unknown field", map[string][]byte{"manifest.json": []byte(`{"version":1,"surprise":true}`)}, false},
		{"oversized content", map[string][]byte{"manifest.json": []byte(strings.Repeat(" ", hostedManifestLimit+1))}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			archive := hostedZIP(t, test.files)
			artifact := fixture.Artifacts[0]
			artifact.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(archive))
			proof, err := decodeHostedArtifact(archive, artifact)
			if (err == nil) != test.isValid {
				t.Fatalf("valid=%v, proof=%+v, error=%v", test.isValid, proof, err)
			}
			if test.isValid && proof.Head != fixture.Manifests[0].Head {
				t.Fatalf("head lost: %+v", proof)
			}
		})
	}
	archive := hostedZIP(t, map[string][]byte{"manifest.json": manifest})
	if _, err := decodeHostedArtifact(archive, fixture.Artifacts[0]); err == nil {
		t.Fatal("wrong artifact digest was accepted")
	}
	artifact := fixture.Artifacts[0]
	artifact.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("not ZIP")))
	if _, err := decodeHostedArtifact([]byte("not ZIP"), artifact); err == nil {
		t.Fatal("invalid ZIP accepted")
	}
}
