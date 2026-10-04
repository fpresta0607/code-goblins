package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

func TestPreCompactWritesAFreshCheckpointOnlyForThePrimarySession(t *testing.T) {
	for _, test := range []struct {
		name        string
		session     string
		role        string
		isGate      bool
		hasLock     bool
		isPrimary   bool
		shouldWrite bool
	}{
		{name: "primary", session: "s1", hasLock: true, isPrimary: true, shouldWrite: true},
		{name: "other session", session: "s2", hasLock: true, isPrimary: true},
		{name: "no lock", session: "s1", isPrimary: true},
		{name: "empty session", hasLock: true, isPrimary: true},
		{name: "goblin", session: "s1", role: "goblin", hasLock: true, isPrimary: true},
		{name: "gate", session: "s1", isGate: true, hasLock: true, isPrimary: true},
		{name: "not primary", session: "s1", hasLock: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := newPrimaryHome(t)
			stateDirectory := filepath.Join(directory, "state")
			checkpoint := filepath.Join(stateDirectory, digest.CheckpointFile)
			if !test.isPrimary {
				if err := os.Remove(filepath.Join(directory, "AGENTS.md")); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("CFO_ROLE", test.role)
			if test.isGate {
				t.Setenv(gateAgentVariable, "1")
			}
			setAncestorPID(t, os.Getpid())
			if test.hasLock {
				if _, err := lock.AcquireOwner(stateDirectory, os.Getpid(), "s1"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lock.Release(stateDirectory) })
			}

			var stdout, stderr bytes.Buffer
			exit := runHook("pre-compact", strings.NewReader(`{"session_id":"`+test.session+`","trigger":"manual"}`), &stdout, &stderr)

			if exit != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("pre-compact: exit %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
			}
			data, err := os.ReadFile(checkpoint)
			if test.shouldWrite {
				if err != nil || !strings.Contains(string(data), "== FLEET ==") {
					t.Fatalf("checkpoint = %q, error = %v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("another session wrote a checkpoint: %v", err)
			}
		})
	}
}

func TestPreCompactLeavesCompactionRunningWhenTheCheckpointCannotBeWritten(t *testing.T) {
	directory := newPrimaryHome(t)
	stateDirectory := filepath.Join(directory, "state")
	setAncestorPID(t, os.Getpid())
	if _, err := lock.AcquireOwner(stateDirectory, os.Getpid(), "s1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(stateDirectory) })
	if err := os.Mkdir(filepath.Join(stateDirectory, digest.CheckpointFile), 0o700); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := runHook("pre-compact", strings.NewReader(`{"session_id":"s1"}`), &stdout, &stderr)

	if exit != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("pre-compact: exit %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
	}
}
