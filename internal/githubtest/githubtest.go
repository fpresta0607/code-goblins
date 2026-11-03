// Package githubtest stands in for GitHub's API in tests: a githubapi client
// whose every request a test's own handler answers, inside the test's own
// goroutine, so no request leaves the process and no gh runs.
package githubtest

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/fpresta0607/code-goblins/internal/githubapi"
)

// Client is a githubapi client whose requests handler answers. Its token is
// the environment's GH_TOKEN of a stand-in environment, so it never asks gh
// for one.
func Client(handler http.Handler) *githubapi.Client {
	client := githubapi.New(nil)
	client.BaseURL = "https://api.github.test"
	client.HTTP = &http.Client{Transport: inProcess{handler: handler}}
	client.Getenv = func(name string) string {
		if name == "GH_TOKEN" {
			return "stand-in-token"
		}
		return ""
	}
	return client
}

// inProcess answers each request with handler, on the caller's goroutine.
type inProcess struct {
	handler http.Handler
}

func (p inProcess) RoundTrip(request *http.Request) (*http.Response, error) {
	// A server's handler always has a body to read, which a GET the client
	// sends has not.
	served := request.Clone(request.Context())
	if served.Body == nil {
		served.Body = http.NoBody
	}
	recorder := httptest.NewRecorder()
	p.handler.ServeHTTP(recorder, served)
	if recorder.Header().Get(timedOut) != "" {
		return nil, context.DeadlineExceeded
	}
	response := recorder.Result()
	response.Request = request
	return response, nil
}

// timedOut is the header TimeOut marks an answer the client never gets with.
const timedOut = "Githubtest-Timed-Out"

// TimeOut answers nothing: the request fails as one whose time ran out does,
// with context.DeadlineExceeded.
func TimeOut(w http.ResponseWriter) {
	w.Header().Set(timedOut, "true")
}

// Query is the GraphQL query and variables request posts.
func Query(request *http.Request) (query string, variables map[string]any, err error) {
	var asked struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(request.Body).Decode(&asked); err != nil {
		return "", nil, fmt.Errorf("read the GraphQL request: %w", err)
	}
	return asked.Query, asked.Variables, nil
}

// Answer writes data as a GraphQL answer's data.
func Answer(w http.ResponseWriter, data any) {
	Write(w, http.StatusOK, map[string]any{"data": data})
}

// Refuse answers with status and GitHub's message, as GitHub refuses a
// request.
func Refuse(w http.ResponseWriter, status int, message string) {
	Write(w, status, map[string]any{"message": message})
}

// Write answers with status and body as JSON.
func Write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// PullRequestNodes is pulls, a JSON array of pull requests in the shape gh pr
// list --json printed them, as GitHub's GraphQL API gives the same pull
// requests (PullRequestNode).
func PullRequestNodes(pulls []byte) ([]map[string]any, error) {
	var flat []map[string]any
	if err := json.Unmarshal(pulls, &flat); err != nil {
		return nil, err
	}
	nodes := make([]map[string]any, 0, len(flat))
	for _, pull := range flat {
		nodes = append(nodes, PullRequestNode(pull))
	}
	return nodes, nil
}

// PullRequestNode is pull, in the shape gh pr view --json printed it, as
// GitHub's GraphQL API gives the same pull request: its checks as the check
// rollup of its head's commit, each check run with the workflow it belongs
// to, each commit status started when it was set, and its labels as nodes.
func PullRequestNode(pull map[string]any) map[string]any {
	node := map[string]any{}
	for key, value := range pull {
		node[key] = value
	}
	labels, _ := pull["labels"].([]any)
	node["labels"] = map[string]any{"nodes": append([]any{}, labels...)}
	var rollup any
	if checks, isListed := pull["statusCheckRollup"].([]any); isListed {
		var contexts []any
		for _, entry := range checks {
			check, _ := entry.(map[string]any)
			context := map[string]any{}
			for key, value := range check {
				context[key] = value
			}
			delete(context, "workflowName")
			context["checkSuite"] = map[string]any{"workflowRun": nil}
			if workflow, _ := check["workflowName"].(string); workflow != "" {
				context["checkSuite"] = map[string]any{"workflowRun": map[string]any{"workflow": map[string]any{"name": workflow}}}
			}
			if check["__typename"] == "StatusContext" {
				delete(context, "startedAt")
				context["createdAt"] = check["startedAt"]
			}
			contexts = append(contexts, context)
		}
		rollup = map[string]any{"contexts": map[string]any{"nodes": append([]any{}, contexts...)}}
	}
	delete(node, "statusCheckRollup")
	node["commits"] = map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{"statusCheckRollup": rollup}}}}
	return node
}

// runFields are the names gh run list --json gives a workflow run's fields,
// each with the name GitHub's API gives the same field.
var runFields = map[string]string{"databaseId": "id", "workflowName": "name", "displayTitle": "display_title", "headSha": "head_sha", "url": "html_url", "attempt": "run_attempt", "startedAt": "run_started_at", "updatedAt": "updated_at"}

// WorkflowRuns is runs, a JSON array of workflow runs in the shape gh run
// list --json printed them, as GitHub's API lists the same runs.
func WorkflowRuns(runs []byte) (map[string]any, error) {
	var listed []map[string]any
	if err := json.Unmarshal(runs, &listed); err != nil {
		return nil, err
	}
	converted := make([]map[string]any, 0, len(listed))
	for _, run := range listed {
		converted = append(converted, WorkflowRun(run))
	}
	return map[string]any{"total_count": len(converted), "workflow_runs": converted}, nil
}

// WorkflowRun is run, a workflow run in the shape gh run view --json printed
// it, as GitHub's API gives the same run.
func WorkflowRun(run map[string]any) map[string]any {
	converted := map[string]any{}
	for key, value := range run {
		converted[cmp.Or(runFields[key], key)] = value
	}
	return converted
}
