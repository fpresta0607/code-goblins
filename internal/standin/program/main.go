// Command standin is the program this repository's tests start where a test
// needs some program to start: a cfo.exe an install downloads, a
// no-mistakes.exe it updates, a harness on PATH. It is one small program,
// built once for each Go toolchain and reused (see standin.Program), so the
// file antivirus meets is one it has met before, never a new unsigned
// program for each test run.
//
// It appends its name and arguments to the file CFO_STANDIN_RECORD names,
// then does what the first rule of CFO_STANDIN_RULES that matches its
// arguments says, and with no such rule exits 0. standin.Env writes both
// variables. It opens no connection and starts no program, and the only
// files it writes are that record and the copies of itself a rule names, as
// an install puts its program in a home.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// misuse is the exit code of a stand-in that could not do what it was told,
// apart from any code a rule gives.
const misuse = 125

// rule is what the stand-in does for one command. It is standin.Rule as the
// program reads it.
type rule struct {
	Args   string   `json:"args"`
	Prefix bool     `json:"prefix"`
	Any    bool     `json:"any"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
	Exit   int      `json:"exit"`
	Hold   bool     `json:"hold"`
	CopyTo []string `json:"copy_to"`
}

func main() {
	if record := os.Getenv("CFO_STANDIN_RECORD"); record != "" {
		name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
		file, err := os.OpenFile(record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "standin:", err)
			os.Exit(misuse)
		}
		_, _ = fmt.Fprintf(file, "%s\r\n", strings.Join(append([]string{name}, os.Args[1:]...), " "))
		_ = file.Close()
	}
	var rules []rule
	if text := os.Getenv("CFO_STANDIN_RULES"); text != "" {
		if err := json.Unmarshal([]byte(text), &rules); err != nil {
			fmt.Fprintln(os.Stderr, "standin: CFO_STANDIN_RULES:", err)
			os.Exit(misuse)
		}
	}
	command := strings.Join(os.Args[1:], " ")
	for _, rule := range rules {
		if !rule.Any && rule.Args != command && !(rule.Prefix && strings.HasPrefix(command, rule.Args+" ")) {
			continue
		}
		for _, path := range rule.CopyTo {
			if err := copySelf(path); err != nil {
				fmt.Fprintln(os.Stderr, "standin:", err)
				os.Exit(misuse)
			}
		}
		_, _ = os.Stdout.WriteString(rule.Stdout)
		_, _ = os.Stderr.WriteString(rule.Stderr)
		if rule.Hold {
			time.Sleep(time.Hour)
		}
		os.Exit(rule.Exit)
	}
}

// copySelf writes this program to path, making its folder.
func copySelf(path string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(self)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o755)
}
