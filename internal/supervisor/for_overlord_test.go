package supervisor

import (
	"encoding/json"
	"testing"
)

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
			store, _ := testStore(t)
			store.db.Questions = []Question{c.question}
			if err := store.save(); err != nil {
				t.Fatal(err)
			}

			view, err := (&Service{Store: store}).Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			var sent struct {
				Questions []struct {
					ID   string `json:"id"`
					Task string `json:"task"`
				} `json:"questions"`
			}
			if err := json.Unmarshal(encoded, &sent); err != nil {
				t.Fatal(err)
			}

			// The native notification path selects CFO questions by this wire marker.
			if len(sent.Questions) != 1 || sent.Questions[0].ID != c.question.ID || (sent.Questions[0].Task == "") != c.want {
				t.Errorf("snapshot question = %+v, want Overlord ownership %v", sent.Questions, c.want)
			}
		})
	}
}
