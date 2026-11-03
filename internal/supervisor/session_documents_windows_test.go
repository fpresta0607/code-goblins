package supervisor

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func TestSessionDocumentsRefuseJunctionEscapes(t *testing.T) {
	board, meta, transcript := documentBoard(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private.pdf"), documentPDF(), 0o600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(meta.Worktree, "linked")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if output, err := execx.CommandContext(ctx, "cmd.exe", "/d", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create disposable junction: %v %s", err, output)
	}
	file := filepath.Join(junction, "private.pdf")
	documentReference(t, transcript, file)

	response := requestDocument(board, meta, file, "document-token")

	if response.Code != http.StatusForbidden || response.Body.String() != "{\"error\":\""+UNAVAILABLE_DOCUMENT+"\"}" {
		t.Fatalf("junction escaped document scope: %d %s", response.Code, response.Body.String())
	}
}
