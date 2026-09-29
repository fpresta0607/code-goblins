package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResumeUsesRecordedConversationReportsEveryTaskAndOffersTerminal(t *testing.T) {
	for _, hasConversation := range []bool{true, false} {
		f := newSessionFixture(t)
		restarted, recovered := false, false
		board := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodPost && (r.Header.Get("X-CFO-Token") != "instance" || r.Header.Get("Origin") == "") {
				t.Error("missing request protection")
			}
			switch r.URL.Path {
			case "/api/snapshot":
				_, _ = io.WriteString(w, `{"instance":"instance"}`)
			case "/api/cfo/resume":
				if !hasConversation {
					w.WriteHeader(409)
					_, _ = io.WriteString(w, `{"error":"no exact conversation recorded"}`)
					return
				}
				_, _ = io.WriteString(w, `{"session":"abc01234-5678-9012-abcd-012345678901","terminal":"cfo","identity":"record"}`)
			case "/api/cfo/restart":
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["identity"] != "record" {
					t.Error("wrong restart identity")
				}
				restarted = true
				_, _ = io.WriteString(w, `{"restarted":true}`)
			case "/api/tasks/resume":
				recovered = true
				_, _ = io.WriteString(w, `[{"id":"one","status":"Resumed","detail":"kept worktree"},{"id":"two","status":"Waiting for memory","detail":"retry later"}]`)
			default:
				http.NotFound(w, r)
			}
		}))
		defer board.Close()
		f.record(board.URL)
		exit, stdout, stderr := f.launch("resume")
		if restarted != hasConversation || !recovered || !strings.Contains(stdout, "Waiting for memory") || len(f.nativeStarts) != 0 {
			t.Fatalf("restart=%t recovery=%t starts=%q output=%s %s", restarted, recovered, f.nativeStarts, stdout, stderr)
		}
		if hasConversation && (exit != 0 || len(f.nativeAttached) != 1) {
			t.Fatalf("exit=%d attached=%q stderr=%s", exit, f.nativeAttached, stderr)
		}
		if !hasConversation && (exit != 1 || len(f.nativeAttached) != 0 || !strings.Contains(stderr, "no exact conversation")) {
			t.Fatalf("exit=%d attached=%q stderr=%s", exit, f.nativeAttached, stderr)
		}
	}
}
