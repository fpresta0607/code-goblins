package connections

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestCodexHealthReportUsesRuntimeAndFollowsPagesWithoutRunningAModel(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	errors := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(server)
		encoder := json.NewEncoder(server)
		page := 0
		for scanner.Scan() {
			var request struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
				errors <- err
				return
			}
			var result json.RawMessage
			switch request.Method {
			case "initialize":
				result = json.RawMessage(`{}`)
			case "initialized":
				continue
			case "thread/start":
				var params struct {
					Ephemeral bool   `json:"ephemeral"`
					Approval  string `json:"approvalPolicy"`
					Sandbox   string `json:"sandbox"`
				}
				_ = json.Unmarshal(request.Params, &params)
				if !params.Ephemeral || params.Approval != "never" || params.Sandbox != "read-only" {
					errors <- fmt.Errorf("unsafe check thread: %+v", params)
					return
				}
				result = json.RawMessage(`{"thread":{"id":"scratch"}}`)
			case "mcpServerStatus/list":
				page++
				if page == 1 {
					result = json.RawMessage(`{"data":[{"name":"working","runtimeStatus":"connected","authStatus":"bearerToken"}],"nextCursor":"second"}`)
				} else {
					result = json.RawMessage(`{"data":[{"name":"cached","runtimeStatus":null,"authStatus":"oAuth","tools":{"cached":{}}}],"nextCursor":null}`)
				}
			default:
				errors <- fmt.Errorf("unexpected model or tool request: %s", request.Method)
				return
			}
			if err := encoder.Encode(struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
			}{request.ID, result}); err != nil {
				errors <- err
				return
			}
			if page == 2 {
				errors <- nil
				return
			}
		}
		errors <- scanner.Err()
	}()
	entries, err := codexReport(context.Background(), client, client, "scratch")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Status != "connected" || entries[1].Status != "unverified" {
		t.Fatalf("wrong runtime evidence: %+v", entries)
	}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
}
