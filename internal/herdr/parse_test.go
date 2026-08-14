package herdr

import "testing"

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Target
		wantErr bool
	}{
		{name: "pane identifier retains later colons", raw: "default:w1:p2", want: Target{Session: "default", Pane: "w1:p2"}},
		{name: "named session", raw: "fleet:workspace:pane", want: Target{Session: "fleet", Pane: "workspace:pane"}},
		{name: "missing separator", raw: "default", wantErr: true},
		{name: "missing session", raw: ":w1:p2", wantErr: true},
		{name: "missing pane", raw: "default:", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseTarget(test.raw)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseTarget(%q) error = %v, want error = %t", test.raw, err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("ParseTarget(%q) = %#v, want %#v", test.raw, got, test.want)
			}
		})
	}
}

func TestTargetString(t *testing.T) {
	target := Target{Session: "default", Pane: "w1:p2"}
	if got, want := target.String(), "default:w1:p2"; got != want {
		t.Errorf("Target.String() = %q, want %q", got, want)
	}
}
