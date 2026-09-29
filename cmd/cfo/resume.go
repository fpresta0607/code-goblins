package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func runResume(stdout, stderr io.Writer, runtime commandRuntime) int {
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	board, _, ok := launchBoard(context.Background(), runtime, h, stdout, stderr)
	if !ok {
		return 1
	}
	var snapshot struct {
		Instance string `json:"instance"`
	}
	data, err := resumeRequest(board, "/api/snapshot", "", nil, 5*time.Second)
	if err == nil {
		err = json.Unmarshal(data, &snapshot)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var record supervisor.CFOResume
	data, restartErr := resumeRequest(board, "/api/cfo/resume", "", nil, 5*time.Second)
	if restartErr == nil {
		restartErr = json.Unmarshal(data, &record)
	}
	if restartErr == nil {
		fmt.Fprintln(stdout, "Resuming the recorded CFO conversation. The current response will be interrupted; goblins keep running.")
		body, err := json.Marshal(struct {
			Identity string `json:"identity"`
		}{record.Identity})
		restartErr = err
		if restartErr == nil {
			_, restartErr = resumeRequest(board, "/api/cfo/restart", snapshot.Instance, body, 2*time.Minute+5*time.Second)
		}
	}
	if restartErr != nil {
		fmt.Fprintf(stderr, "CFO  Needs a hand: %v\n", restartErr)
	} else {
		fmt.Fprintf(stdout, "CFO  Resumed conversation %s\n", record.Session)
	}
	var tasks []supervisor.RecoveryResult
	data, taskErr := resumeRequest(board, "/api/tasks/resume", snapshot.Instance, []byte(`{}`), 31*time.Minute)
	if taskErr == nil {
		taskErr = json.Unmarshal(data, &tasks)
	}
	if taskErr != nil {
		fmt.Fprintln(stderr, "Task recovery:", taskErr)
	}
	for _, task := range tasks {
		fmt.Fprintf(stdout, "%s  %s\n  %s\n", task.ID, task.Status, task.Detail)
	}
	if restartErr != nil {
		return 1
	}
	choice, err := runtime.choose(stdout, fmt.Sprintf("Recovery report\nBoard \x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\  (Ctrl+click to open)", board, board), []string{"Open CFO terminal (Default)", "[B] Open the board"}, 0)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if choice == 1 {
		if err := runtime.openURL(board); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		if code := runtime.attachNative(h.State, record.Terminal, stdout, stderr); code != 0 {
			return code
		}
	}
	if taskErr != nil {
		return 1
	}
	return 0
}

func resumeRequest(board, path, instance string, input []byte, timeout time.Duration) (json.RawMessage, error) {
	method := http.MethodGet
	var body io.Reader
	if input != nil {
		if instance == "" {
			return nil, errors.New("the supervisor did not provide its instance token; update and restart the supervisor")
		}
		body = bytes.NewReader(input)
		method = http.MethodPost
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, board+path, body)
	if err != nil {
		return nil, err
	}
	if input != nil {
		request.Header.Set("Origin", board)
		request.Header.Set("X-CFO-Token", instance)
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var problem struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&problem) != nil || problem.Error == "" {
			return nil, fmt.Errorf("the board returned HTTP %d", response.StatusCode)
		}
		return nil, errors.New(problem.Error)
	}
	var output json.RawMessage
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&output)
	return output, err
}
