package connections

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"
)

func checkClaude(ctx context.Context, dir string, env, launch []string) ([]Entry, error) {
	args := []string{"--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--no-session-persistence", "--settings", `{"disableAllHooks":true}`}
	args = append(args, launch...)
	return harnessReport(ctx, "claude", args, dir, env, func(output io.Reader, input io.Writer) ([]Entry, error) { return claudeReport(ctx, output, input) })
}

func claudeReport(ctx context.Context, output io.Reader, input io.Writer) ([]Entry, error) {
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	encoder := json.NewEncoder(input)
	identifier := 0
	call := func(subtype string, result interface{}) error {
		identifier++
		id := strconv.Itoa(identifier)
		request := struct {
			Type    string            `json:"type"`
			ID      string            `json:"request_id"`
			Request map[string]string `json:"request"`
		}{"control_request", id, map[string]string{"subtype": subtype}}
		if err := encoder.Encode(request); err != nil {
			return errors.New("Claude connection check stopped.")
		}
		for scanner.Scan() {
			var message struct {
				Type     string `json:"type"`
				Response struct {
					Subtype  string          `json:"subtype"`
					ID       string          `json:"request_id"`
					Response json.RawMessage `json:"response"`
				} `json:"response"`
			}
			if json.Unmarshal(scanner.Bytes(), &message) != nil || message.Type != "control_response" || message.Response.ID != id {
				continue
			}
			if message.Response.Subtype != "success" {
				return errors.New("Claude could not report connection health.")
			}
			if err := json.Unmarshal(message.Response.Response, result); err != nil {
				return errors.New("Claude returned an unreadable connection report.")
			}
			return nil
		}
		return errors.New("Claude connection check stopped.")
	}
	var initialized json.RawMessage
	if err := call("initialize", &initialized); err != nil {
		return nil, err
	}
	for {
		var report struct {
			Servers []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"mcpServers"`
		}
		if err := call("mcp_status", &report); err != nil {
			return nil, err
		}
		entries := []Entry{}
		isPending := false
		for _, server := range report.Servers {
			if !validName(server.Name) {
				continue
			}
			status := map[string]string{"connected": "connected", "failed": "failed", "needs-auth": "unauthorized", "pending": "checking", "disabled": "disabled"}[server.Status]
			if status == "" {
				status = "unverified"
			}
			isPending = isPending || status == "checking"
			entries = append(entries, Entry{ID: "mcp:" + server.Name, Name: server.Name, Kind: "mcp", Status: status, Source: "Claude", Detail: statusDetail(status)})
		}
		if !isPending {
			return entries, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
