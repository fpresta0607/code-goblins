package supervisor

import "testing"

func TestOnlyAQuestionTheCFOAsksIsTheOverlordsToAnswer(t *testing.T) {
	for name, c := range map[string]struct {
		question Question
		want     bool
	}{
		"the CFO asks him":                     {Question{ID: "pick-a-store"}, true},
		"the CFO passes a goblin's ask up":     {Question{ID: "from-billing", Text: "billing asks: which store?"}, true},
		"a goblin asks the CFO":                {Question{ID: "notify-billing-7", Task: "billing", Generation: "g1", Seq: 7}, false},
		"a goblin asks beside its open page":   {Question{ID: "notify-billing-8", Task: "billing", Generation: "g1", Seq: 8, Page: "plan-billing"}, false},
		"a goblin's question the CFO answered": {Question{ID: "notify-billing-6", Task: "billing", Status: "succeeded", AnsweredBy: "cfo"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			got := forOverlord(c.question)

			// Assert
			if got != c.want {
				t.Errorf("forOverlord = %v, want %v", got, c.want)
			}
		})
	}
}
