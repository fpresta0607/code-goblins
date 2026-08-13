package guard

import "testing"

func TestClassifyArm(t *testing.T) {
	tests := []struct {
		command  string
		wantDeny bool
		wantCode string
	}{
		{command: "echo hi", wantDeny: false},
		{command: "cfo watch", wantDeny: true, wantCode: "watcher-direct"},
		{command: "cfo watch &", wantDeny: true, wantCode: "watcher-background"},
		{command: "cfo watch | tee log", wantDeny: true, wantCode: "watcher-pipeline"},
		{command: "cfo watch > out.txt", wantDeny: true, wantCode: "watcher-redirection"},
		{command: "cd x && cfo watch", wantDeny: true, wantCode: "watcher-bundled"},
		{command: "$(cfo watch)", wantDeny: true, wantCode: "watcher-nested"},
		{command: "pkill -f cfo watch", wantDeny: true, wantCode: "broad-watcher-kill"},
		{command: "bin/fm-watch.sh", wantDeny: true, wantCode: "watcher-direct"},
		{command: `echo $'cfo watch'`, wantDeny: true, wantCode: "unclassifiable-protected-command"},
		{command: "cfo watchdog-config", wantDeny: false},
		{command: "git log --oneline", wantDeny: false},
		{command: `.\cfo.exe watch`, wantDeny: true, wantCode: "watcher-direct"},
		{command: "cfo.exe watch &", wantDeny: true, wantCode: "watcher-background"},
		{command: `C:\dev\code-goblins\cfo.exe watch`, wantDeny: true, wantCode: "watcher-direct"},
		{command: `grep $'\t' file`, wantDeny: false},
	}

	reasons := map[string]string{
		"broad-watcher-kill":               "a broad process kill in the same command as a watcher invocation takes supervision down along with its intended target",
		"watcher-background":               "backgrounding the watcher orphans it from the Stop-owned auto-arm that is supposed to host it",
		"watcher-pipeline":                 "piping the watcher's output swallows the wake reason the auto-arm returns",
		"watcher-redirection":              "redirecting the watcher's output swallows the wake reason the auto-arm returns",
		"watcher-bundled":                  "bundling the watcher with other statements hides which half of the command supervision depends on",
		"watcher-nested":                   "nesting the watcher inside a substitution or an interpreter wrapper hides it from this guard's diagnostics",
		"unclassifiable-protected-command": "this command quotes a watcher invocation in a form this guard cannot classify safely",
		"watcher-direct":                   "the watcher is armed by the Stop-owned auto-arm hook, never from the agent shell",
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			code, reason, deny := ClassifyArm(tt.command)
			if deny != tt.wantDeny {
				t.Fatalf("ClassifyArm(%q) deny = %v, want %v (code=%q reason=%q)", tt.command, deny, tt.wantDeny, code, reason)
			}
			if !tt.wantDeny {
				return
			}
			if code != tt.wantCode {
				t.Errorf("ClassifyArm(%q) code = %q, want %q", tt.command, code, tt.wantCode)
			}
			if wantReason := reasons[tt.wantCode]; reason != wantReason {
				t.Errorf("ClassifyArm(%q) reason = %q, want %q", tt.command, reason, wantReason)
			}
		})
	}
}

func TestClassifyCd(t *testing.T) {
	const wantReason = "Claude Code's Bash tool keeps its working directory between calls, so this relocation would outlive the tool call"

	tests := []struct {
		command  string
		wantDeny bool
	}{
		{command: `cd C:\other`, wantDeny: true},
		{command: "pushd ..", wantDeny: true},
		{command: "cd sub && go test ./...", wantDeny: true},
		{command: "(cd sub && make)", wantDeny: false},
		{command: "go test ./... && (cd sub)", wantDeny: false},
		{command: "go test ./... && cd sub", wantDeny: true},
		{command: "go test ./... ; popd", wantDeny: true},
		{command: `Set-Location C:\`, wantDeny: true},
		{command: `(Set-Location C:\)`, wantDeny: true},
		{command: "echo cd", wantDeny: false},
		{command: `git commit -m "wip; cd later"`, wantDeny: false},
		{command: `echo "a | popd"`, wantDeny: false},
		{command: `git -C C:\x status`, wantDeny: false},
		{command: "go test ./...\ncd sub", wantDeny: true},
		{command: "go test ./...\ngo build ./...", wantDeny: false},
		{command: "sleep 5 & cd sub", wantDeny: true},
		{command: ") ; cd x", wantDeny: true},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			code, reason, deny := ClassifyCd(tt.command)
			if deny != tt.wantDeny {
				t.Fatalf("ClassifyCd(%q) deny = %v, want %v (code=%q reason=%q)", tt.command, deny, tt.wantDeny, code, reason)
			}
			if !tt.wantDeny {
				return
			}
			if code != "cwd-relocation" {
				t.Errorf("ClassifyCd(%q) code = %q, want %q", tt.command, code, "cwd-relocation")
			}
			if reason != wantReason {
				t.Errorf("ClassifyCd(%q) reason = %q, want %q", tt.command, reason, wantReason)
			}
		})
	}
}
