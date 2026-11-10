package memory

import (
	"errors"
	"slices"
	"strings"
	"time"
)

// EvalCase is one question with the items that answer it. An expected entry
// is an item id, or "text:" and a phrase that the answering item's text holds.
type EvalCase struct {
	Question string   `json:"question"`
	Project  string   `json:"project,omitempty"`
	Expect   []string `json:"expect"`
}

// EvalMiss is a question whose answer was not in the top results.
type EvalMiss struct {
	Question string   `json:"question"`
	Expect   []string `json:"expect"`
	Got      []string `json:"got"`
}

// EvalReport scores recall over a question set at one depth.
type EvalReport struct {
	Questions     int        `json:"questions"`
	Depth         int        `json:"depth"`
	HitsAtOne     int        `json:"hits_at_1"`
	HitsAtDepth   int        `json:"hits_at_depth"`
	MeanRank      float64    `json:"mean_reciprocal_rank"`
	Indexed       int        `json:"indexed"`
	BuildMillis   float64    `json:"build_ms"`
	QueryP50      float64    `json:"query_ms_p50"`
	QueryP95      float64    `json:"query_ms_p95"`
	QueryMax      float64    `json:"query_ms_max"`
	Misses        []EvalMiss `json:"misses"`
	IndexProblems []string   `json:"index_problems,omitempty"`
}

func answers(item Recalled, expect []string) bool {
	return slices.ContainsFunc(expect, func(expected string) bool {
		if phrase, isPhrase := strings.CutPrefix(expected, "text:"); isPhrase {
			return phrase != "" && strings.Contains(item.Title+"\n"+item.Text, phrase)
		}
		return item.ID == expected
	})
}

// Evaluate asks every case at depth and reports where its answer ranked.
// The index's own build time is the caller's to supply.
func (index *Index) Evaluate(cases []EvalCase, depth int, build time.Duration) (EvalReport, error) {
	if len(cases) == 0 {
		return EvalReport{}, errors.New("the evaluation set has no questions")
	}
	report := EvalReport{Questions: len(cases), Depth: depth, Indexed: len(index.Items), BuildMillis: milliseconds(build), Misses: []EvalMiss{}, IndexProblems: index.Problems}
	var timings []float64
	for _, test := range cases {
		if len(test.Expect) == 0 {
			return EvalReport{}, errors.New("an evaluation question names no expected answer: " + test.Question)
		}
		result, err := index.Recall(RecallQuery{Question: test.Question, Project: test.Project, Limit: depth})
		if err != nil {
			return EvalReport{}, err
		}
		timings = append(timings, milliseconds(result.Elapsed))
		rank := slices.IndexFunc(result.Items, func(item Recalled) bool { return answers(item, test.Expect) })
		if rank == 0 {
			report.HitsAtOne++
		}
		if rank >= 0 {
			report.HitsAtDepth++
			report.MeanRank += 1 / float64(rank+1)
			continue
		}
		miss := EvalMiss{Question: test.Question, Expect: test.Expect, Got: []string{}}
		for _, item := range result.Items {
			miss.Got = append(miss.Got, item.ID)
		}
		report.Misses = append(report.Misses, miss)
	}
	report.MeanRank /= float64(len(cases))
	slices.Sort(timings)
	report.QueryP50 = timings[len(timings)/2]
	report.QueryP95 = timings[min(len(timings)-1, len(timings)*95/100)]
	report.QueryMax = timings[len(timings)-1]
	return report, nil
}

func milliseconds(duration time.Duration) float64 { return float64(duration.Microseconds()) / 1000 }
