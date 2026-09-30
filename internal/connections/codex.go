package connections

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
)

func checkCodex(ctx context.Context, dir string, env, overrides []string) ([]Entry, error) {
	args := append([]string{"app-server", "--stdio"}, overrides...)
	return harnessReport(ctx, "codex", args, dir, env, func(output io.Reader, input io.Writer) ([]Entry, error) { return codexReport(ctx, output, input, dir) })
}

func codexReport(ctx context.Context, output io.Reader, input io.Writer, dir string) ([]Entry, error) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	encoder := json.NewEncoder(input)
	identifier := 0
	call := func(method string, params interface{}, result interface{}) error {
		identifier++
		request := struct {
			ID     int         `json:"id"`
			Method string      `json:"method"`
			Params interface{} `json:"params"`
		}{identifier, method, params}
		if err := encoder.Encode(request); err != nil {
			return errors.New("Codex connection check stopped.")
		}
		for scanner.Scan() {
			var message struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(scanner.Bytes(), &message) != nil {
				continue
			}
			if message.ID != identifier {
				continue
			}
			if len(message.Error) > 0 {
				return errors.New("Codex could not report connection health.")
			}
			if err := json.Unmarshal(message.Result, result); err != nil {
				return errors.New("Codex returned an unreadable connection report.")
			}
			return nil
		}
		return errors.New("Codex connection check stopped.")
	}
	var initialized json.RawMessage
	if err := call("initialize", map[string]interface{}{"clientInfo": map[string]string{"name": "code_goblins_connections", "version": "1"}, "capabilities": nil}, &initialized); err != nil {
		return nil, err
	}
	if err := encoder.Encode(map[string]string{"method": "initialized"}); err != nil {
		return nil, errors.New("Codex connection check stopped.")
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := call("thread/start", map[string]interface{}{"cwd": dir, "ephemeral": true, "approvalPolicy": "never", "sandbox": "read-only"}, &started); err != nil {
		return nil, err
	}
	if started.Thread.ID == "" {
		return nil, errors.New("Codex did not start a connection check.")
	}
	for {
		entries := []Entry{}
		cursor := ""
		isStarting := false
		for page := 0; page < 20; page++ {
			var report struct {
				Data       []codexStatus `json:"data"`
				NextCursor string        `json:"nextCursor"`
			}
			params := map[string]interface{}{"threadId": started.Thread.ID, "detail": "toolsAndAuthOnly", "limit": 100}
			if cursor != "" {
				params["cursor"] = cursor
			}
			if err := call("mcpServerStatus/list", params, &report); err != nil {
				return nil, err
			}
			for _, status := range report.Data {
				if !validName(status.Name) {
					continue
				}
				if status.RuntimeStatus == "starting" || status.RuntimeStatus == "notStarted" {
					isStarting = true
				}
				entries = append(entries, codexEntry(status))
			}
			if report.NextCursor == "" {
				break
			}
			if report.NextCursor == cursor || page == 19 {
				return nil, errors.New("Codex connection report exceeded its limit.")
			}
			cursor = report.NextCursor
		}
		if !isStarting {
			return entries, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func validName(name string) bool {
	return name != "" && len(name) <= 128 && strings.IndexFunc(name, unicode.IsControl) < 0 && !strings.Contains(name, "://")
}
