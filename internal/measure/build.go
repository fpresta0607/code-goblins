package measure

import (
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/routing"
)

// claudeImplementAgent is the agent type of the Claude Code sub-agent the
// harness writes for the implement role, ceo- and the role's name.
const claudeImplementAgent = "ceo-implement"

// Input is what one run of a task is measured from: the task's own lane and
// its implement worker's, empty when the goblin implements its own work,
// the spend of its own conversation and of its workers' transcripts, and
// its gate runs' calls and Test steps.
type Input struct {
	Task, Project, Outcome, PR string
	From, To                   time.Time
	Harness                    string
	LeadModel, LeadEffort      string
	ImplementModel             string
	ImplementEffort            string
	Lead                       []Spend
	Workers                    []Worker
	Gate                       []pipeline.GateCalls
	Tests                      []pipeline.GateTest
	Escalations                []string
	Unmeasured                 []string
}

// Build is the record of input's run. The conversation's spend is the
// lead's, and a worker's is the implement role's when it is the harness's
// implement worker: Claude Code's ceo-implement, or a Codex child on the
// task's implement lane, which Codex names by its model and effort alone.
// Every other worker is a helper of its agent type. A gate call is the
// review role's for a review, the verify role's for a Test step's agent,
// the fix role's for a repair of any step, and otherwise a gate step's of
// its purpose. Each run's Test step that passed or failed counts for the
// harness and model of its last test agent.
func Build(input Input) Record {
	record := Record{Task: input.Task, Project: input.Project, Outcome: input.Outcome, PR: input.PR, From: input.From, To: input.To, Escalations: input.Escalations, Unmeasured: input.Unmeasured}
	var roles rows
	for _, spend := range input.Lead {
		roles.add(sessionRole(RoleLead, input.Harness, "", spend, input.LeadModel, input.LeadEffort))
	}
	for _, worker := range input.Workers {
		for _, spend := range worker.Spends {
			isImplement := worker.Agent == claudeImplementAgent || input.Harness == "codex" && input.ImplementModel != "" && routing.SameModel(input.Harness, input.ImplementModel, spend.Model) && spend.Effort == input.ImplementEffort
			if isImplement {
				roles.add(sessionRole(RoleImplement, input.Harness, "", spend, input.ImplementModel, input.ImplementEffort))
			} else {
				roles.add(sessionRole(RoleHelper, input.Harness, worker.Agent, spend, "", ""))
			}
		}
	}
	for _, call := range input.Gate {
		row := Role{Role: RoleGate, Harness: call.Agent, Model: call.Model, Agent: call.Purpose, Calls: call.Calls, Untallied: call.Untallied, Usage: Usage{InputTokens: call.InputTokens, OutputTokens: call.OutputTokens, CacheReadTokens: call.CacheReadTokens}, WallSeconds: float64(call.DurationMS) / 1000}
		switch {
		case call.Purpose == "review" || call.Purpose == "review-coverage":
			row.Role, row.Agent, row.Findings = RoleReview, "", call.Findings
		case call.Purpose == "test":
			row.Role, row.Agent = RoleVerify, ""
		case strings.HasSuffix(call.Purpose, "-fix"):
			row.Role = RoleFix
		}
		roles.add(row)
	}
	for _, test := range input.Tests {
		record.Runs = append(record.Runs, test.Run)
		row := Role{Role: RoleVerify, Harness: test.Agent, Model: test.Model}
		switch test.Status {
		case "completed":
			row.Passed = 1
		case "failed":
			row.Failed = 1
		default:
			continue
		}
		roles.add(row)
	}
	record.Roles = roles
	return record
}

// sessionRole is role's row for one model a conversation spent on: one
// session, its tokens, and the time from its first request to its last. A
// harness that records no effort is given the lane's own when the model is
// the lane's.
func sessionRole(role, harness, agent string, spend Spend, laneModel, laneEffort string) Role {
	effort := spend.Effort
	if effort == "" && laneModel != "" && routing.SameModel(harness, laneModel, spend.Model) {
		effort = laneEffort
	}
	return Role{Role: role, Harness: harness, Model: spend.Model, Effort: effort, Agent: agent, Calls: 1, Usage: spend.Usage, WallSeconds: spend.Last.Sub(spend.First).Seconds()}
}

// rows are a record's roles, one row for each role, harness, model, effort
// and agent, in the order each was first added.
type rows []Role

func (r *rows) add(row Role) {
	index := slices.IndexFunc(*r, func(existing Role) bool { return existing.key() == row.key() })
	if index < 0 {
		*r = append(*r, row)
		return
	}
	(*r)[index].add(row)
}
