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
	// last is the latest report, kept so the check's time limit ends it with
	// what it saw: a server that connected or failed keeps that verdict, and
	// only one still starting reads unverified.
	var last []Entry
	ended := func() []Entry {
		for index := range last {
			if last[index].Status == "checking" {
				last[index].Status, last[index].Detail = "unverified", "Still starting when the check ended."
			}
		}
		return last
	}
	for {
		var report struct {
			Servers []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Tools  []struct {
					Name        string `json:"name"`
					Annotations struct {
						ReadOnly bool `json:"readOnly"`
					} `json:"annotations"`
				} `json:"tools"`
			} `json:"mcpServers"`
		}
		if err := call("mcp_status", &report); err != nil {
			if last != nil && ctx.Err() != nil {
				return ended(), nil
			}
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
			reported := make([]Tool, 0, len(server.Tools))
			for _, tool := range server.Tools {
				reported = append(reported, Tool{Name: tool.Name, ReadOnly: tool.Annotations.ReadOnly})
			}
			entries = append(entries, Entry{ID: "mcp:" + server.Name, Name: server.Name, Kind: "mcp", Status: status, Source: "Claude", Detail: statusDetail(status), Tools: listedTools(reported)})
		}
		if !isPending {
			return entries, nil
		}
		last = entries
		select {
		case <-ctx.Done():
			return ended(), nil
		case <-time.After(250 * time.Millisecond):
		}
	}
}
