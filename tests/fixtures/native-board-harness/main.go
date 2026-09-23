// This deterministic acceptance fixture is built as codex.exe inside an
// isolated test home so Herdr can exercise its native foreground-process
// contract. It is not Codex and never invokes a model or remote API.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("fixture configuration required")
	}
	var c struct{ Root, Home, Project, Session, Pane, Hook, Role, NativeID, ParentHarness, Binary string }
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if !strings.HasPrefix(c.Session, "cfo-board-test-") || !strings.HasPrefix(filepath.Base(c.Root), "cfo-board-windows-") {
		return fmt.Errorf("refusing a non-fixture home or session")
	}
	if c.Role == "" {
		c.Role = "goblin"
	}
	if c.NativeID == "" {
		c.NativeID = "board-worker"
	}
	if c.ParentHarness == "" {
		c.ParentHarness = "claude"
	}
	if c.Role != "cfo" && c.Role != "goblin" {
		return fmt.Errorf("unsupported example role")
	}
	if c.NativeID != "board-worker" && c.NativeID != "board-cfo" {
		return fmt.Errorf("unsupported example session")
	}
	for key, value := range map[string]string{"CFO_HOME": c.Home, "CFO_STATE_OVERRIDE": filepath.Join(c.Home, "state"), "CFO_ROLE": c.Role, "CFO_TASK_ID": "board-fixture", "CFO_SPAWN_GEN": "fixture-1", "CFO_PARENT_SESSION_ID": "board-cfo", "CFO_PARENT_HARNESS": c.ParentHarness, "CFO_ROOT_SESSION_ID": "board-cfo"} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	if c.Role == "cfo" {
		for _, key := range []string{"CFO_TASK_ID", "CFO_SPAWN_GEN", "CFO_PARENT_SESSION_ID", "CFO_PARENT_HARNESS", "CFO_ROOT_SESSION_ID"} {
			_ = os.Unsetenv(key)
		}
	}
	invoke := func(name string, input []byte, args ...string) error {
		command := exec.Command(name, args...)
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", name, err, output)
		}
		return nil
	}
	sequence := 0
	report := func(state string) error {
		sequence++
		return invoke("herdr", nil, "--session", c.Session, "pane", "report-agent", c.Pane, "--source", "cfo-board-fixture", "--agent", "codex", "--state", state, "--seq", strconv.Itoa(sequence), "--agent-session-id", c.NativeID)
	}
	hook := func(name string, turn int) error {
		payload, err := json.Marshal(map[string]string{"hook_event_name": name, "session_id": c.NativeID, "cwd": c.Project, "turn_id": strconv.Itoa(turn), "model": "example (no model calls)"})
		if err != nil {
			return err
		}
		return invoke("powershell.exe", payload, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", c.Hook)
	}
	pidFile, receiptFile := "harness.pid", "receipts.jsonl"
	if c.Role == "cfo" {
		pidFile, receiptFile = "cfo-harness.pid", "cfo-receipts.jsonl"
	}
	if err := os.WriteFile(filepath.Join(c.Root, pidFile), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		return err
	}
	if err := hook("SessionStart", 0); err != nil {
		return err
	}
	if err := hook("UserPromptSubmit", 0); err != nil {
		return err
	}
	if err := report("working"); err != nil {
		return err
	}
	fmt.Printf("Example workspace | %s native terminal\nDeterministic acceptance process. No model calls.\nReady for a submitted message.\n", c.Role)
	input := bufio.NewScanner(os.Stdin)
	input.Buffer(make([]byte, 4096), 32<<10)
	input.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	turn := 0
	for input.Scan() {
		if strings.TrimSpace(input.Text()) == "" {
			continue
		}
		turn++
		text := input.Text()
		receipt, err := json.Marshal(struct {
			Turn int       `json:"turn"`
			Text string    `json:"text"`
			At   time.Time `json:"at"`
		}{turn, text, time.Now().UTC()})
		if err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(c.Root, receiptFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(append(receipt, '\n'))
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := report("working"); err != nil {
			return err
		}
		if err := hook("UserPromptSubmit", turn); err != nil {
			return err
		}
		fmt.Printf("Accepted message %d in the %s process:\n%s\n", turn, c.Role, text)
		// A goblin asks the way a real one does: cfo notify from inside its
		// own pane, so the board's pane proof sees this process as the asker.
		if _, question, ok := strings.Cut(text, "fixture:ask "); ok && c.Role == "goblin" {
			output, err := exec.Command(c.Binary, "notify", "board-fixture", "--blocked", question).CombinedOutput()
			fmt.Printf("cfo notify: %s (error: %v)\n", strings.TrimSpace(string(output)), err)
		}
		// It reports a review page the same way, from its own pane.
		if _, report, ok := strings.Cut(text, "fixture:present "); ok && c.Role == "goblin" {
			id, link, _ := strings.Cut(report, " ")
			output, err := exec.Command(c.Binary, "present", "--id", id, "--task", "board-fixture", "--kind", "review", "--url", link).CombinedOutput()
			fmt.Printf("cfo present: %s (error: %v)\n", strings.TrimSpace(string(output)), err)
		}
		if strings.Contains(text, "fixture:crash-ready") {
			continue
		}
		time.Sleep(1500 * time.Millisecond)
		if err := hook("Stop", turn); err != nil {
			return err
		}
		if err := report("idle"); err != nil {
			return err
		}
	}
	return input.Err()
}
