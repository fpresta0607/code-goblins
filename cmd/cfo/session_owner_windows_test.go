package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

func TestManualDigestUsesItsNativeCustody(t *testing.T) {
	if action := os.Getenv("CFO_DIGEST_OWNER_FIXTURE"); action != "" {
		root := os.Getenv("CFO_DIGEST_OWNER_ROOT")
		state := filepath.Join(root, "state")
		if action == "owner" {
			owner := os.Getpid()
			if os.Getenv("CFO_DIGEST_OWNER_SHIM") == "1" {
				entries, err := proc.Ancestry(owner, 2)
				if err != nil || len(entries) != 2 || !strings.EqualFold(entries[1].ExeBase, "cmd.exe") {
					t.Fatal("the shim fixture did not run under cmd.exe")
				}
				owner = entries[1].PID
			}
			if foreign := os.Getenv("CFO_DIGEST_OWNER_FOREIGN"); foreign != "" {
				var err error
				owner, err = strconv.Atoi(foreign)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := lock.AcquireOwner(state, owner, "registered-fixture"); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Getenv("CFO_DIGEST_OWNER_BINARY"), "-test.run=^TestManualDigestUsesItsNativeCustody$")
			command.Env = append(os.Environ(), "CFO_DIGEST_OWNER_FIXTURE=digest")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("manual digest: %v\n%s", err, output)
			}
			return
		}
		t.Setenv("CFO_HOME", root)
		t.Setenv("CFO_STATE_OVERRIDE", state)
		before, err := os.ReadFile(filepath.Join(state, ".lock"))
		if err != nil {
			t.Fatal(err)
		}
		var out, errs bytes.Buffer
		if exit := run([]string{"session-start"}, &out, &errs); exit != 0 {
			t.Fatalf("session-start exit %d: %s", exit, errs.String())
		}
		if os.Getenv("CFO_DIGEST_OWNER_FOREIGN") != "" {
			after, err := os.ReadFile(filepath.Join(state, ".lock"))
			if err != nil || !bytes.Equal(before, after) || !strings.Contains(out.String(), "READ-ONLY DIGEST") {
				t.Fatal("an unrelated custodian must remain unchanged and the digest read-only")
			}
			if _, isComplete := digest.ReadCompleteMarker(state); isComplete {
				t.Fatal("an unrelated caller wrote the completion marker")
			}
			return
		}
		holder, err := lock.Read(state)
		if err != nil {
			t.Fatal(err)
		}
		owner, isComplete := digest.ReadCompleteMarker(state)
		if strings.Contains(out.String(), "READ-ONLY DIGEST") || !isComplete || owner != holder.PID {
			lines := strings.Split(out.String(), "\n")
			t.Fatalf("native custody pid %d was rejected by its own digest: marker=%d complete=%v\n%s", holder.PID, owner, isComplete, strings.Join(lines[:min(9, len(lines))], "\n"))
		}
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"codex", "node-shim", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"state", "data"} {
				if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			name := "codex.exe"
			if mode == "node-shim" {
				name = "node.exe"
			}
			fixture := filepath.Join(root, name)
			if err := os.WriteFile(fixture, program, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, fixture, "-test.run=^TestManualDigestUsesItsNativeCustody$")
			if mode == "node-shim" {
				command = exec.CommandContext(ctx, "cmd.exe", "/c", fixture, "-test.run=^TestManualDigestUsesItsNativeCustody$")
			}
			for _, entry := range os.Environ() {
				name, _, _ := strings.Cut(entry, "=")
				if name != "CFO_HOME" && name != "CFO_STATE_OVERRIDE" && name != "CFO_TEST_ANCESTOR_PID" {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env, "CFO_DIGEST_OWNER_FIXTURE=owner", "CFO_DIGEST_OWNER_ROOT="+root, "CFO_DIGEST_OWNER_BINARY="+binary)
			if mode == "node-shim" {
				command.Env = append(command.Env, "CFO_DIGEST_OWNER_SHIM=1")
			}
			if mode == "unrelated" {
				foreign := startLiveForeignProcess(t)
				command.Env = append(command.Env, fmt.Sprintf("CFO_DIGEST_OWNER_FOREIGN=%d", foreign.Process.Pid))
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", mode, err, output)
			}
		})
	}
}
