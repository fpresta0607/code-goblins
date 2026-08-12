package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMetaLastKeyWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g1.meta")
	content := "worktree=C:\\wt\\g1\nkind=ship\nworktree=C:\\wt\\g1-respawn\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	kv, err := ReadMeta(path)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if kv["worktree"] != `C:\wt\g1-respawn` {
		t.Errorf("worktree = %q, want the later value", kv["worktree"])
	}
	if kv["kind"] != "ship" {
		t.Errorf("kind = %q, want %q", kv["kind"], "ship")
	}
}

func TestReadMetaIgnoresNonPairLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g1.meta")
	if err := os.WriteFile(path, []byte("# comment\n\nkind=scout\n=orphan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	kv, err := ReadMeta(path)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if len(kv) != 1 || kv["kind"] != "scout" {
		t.Errorf("kv = %v, want only kind=scout", kv)
	}
}

func TestReadMetaValueMayContainEquals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g1.meta")
	if err := os.WriteFile(path, []byte("endpoint=session=s1;pane=p2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	kv, err := ReadMeta(path)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if kv["endpoint"] != "session=s1;pane=p2" {
		t.Errorf("endpoint = %q, split must happen on the first '=' only", kv["endpoint"])
	}
}

func TestReadMetaMissingFile(t *testing.T) {
	_, err := ReadMeta(filepath.Join(t.TempDir(), "absent.meta"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist", err)
	}
}

func TestWriteMetaRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g2.meta")
	in := map[string]string{"kind": "ship", "harness": "claude", "worktree": `C:\wt\g2`}
	if err := WriteMeta(path, in); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	out, err := ReadMeta(path)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round trip lost keys: %v", out)
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("%s = %q, want %q", k, out[k], v)
		}
	}
}

func TestWriteMetaDeterministicOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g3.meta")
	if err := WriteMeta(path, map[string]string{"b": "2", "a": "1"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "a=1\nb=2\n" {
		t.Errorf("file = %q, want sorted keys %q", data, "a=1\nb=2\n")
	}
}
