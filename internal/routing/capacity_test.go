package routing

import (
	"strings"
	"testing"
)

func TestDetectRecognizesTheNativeCodexCapacityRefusal(t *testing.T) {
	banner := "■ Selected model is at capacity. Please try a different model."
	quotes := []string{
		"",
		"    64 +" + banner,
		"-" + banner,
		"review.log:38: " + banner,
		"```text\n" + banner + "\n```",
		"~~~text\n" + banner + "\n~~~",
		"````text\n```nested\n" + banner + "\n````",
		"~~~text\n~~~nested\n" + banner + "\n~~~",
		"• Ran Get-Content review.log\n  └ earlier output\n    " + banner,
		"• Ran Get-Content review.log\n  └ " + banner,
		"> " + banner,
		"CFO: earlier capacity refusal\n  " + banner,
		"Overlord: " + banner,
		"The review log contains: " + banner,
		"Selected model is at capacity. Please try a different model.",
		banner + " was a historical failure.",
		"■ Selected model is at capacity.",
	}
	for _, quote := range quotes {
		t.Run(quote, func(t *testing.T) {
			if fault, evidence, found := Detect(quote); found {
				t.Fatalf("quoted/discussed capacity = (%q, %q), want no fault", fault, evidence)
			}
			for _, refusal := range []string{banner, "  " + banner + "\r"} {
				fault, evidence, found := Detect(quote + "\n\n" + refusal)
				if !found || fault != Provider || evidence != banner {
					t.Errorf("genuine refusal after quote = (%q, %q, %v), want Provider and original banner", fault, evidence, found)
				}
			}
		})
	}
	for _, prefix := range []string{
		"• Ran Get-Content review.log\n  └ earlier output\n    quoted continuation\n",
		"CFO: continue on the same model\n",
		"```an unfinished fence\n",
	} {
		fault, evidence, found := Detect(prefix + banner)
		if !found || fault != Provider || !strings.HasPrefix(evidence, "■ Selected model") {
			t.Errorf("native refusal after prior text = (%q, %q, %v), want Provider", fault, evidence, found)
		}
	}
}
