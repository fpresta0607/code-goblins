package gatehome

import "testing"

func TestRepointNamesTheSamePlaceInTheMovedHome(t *testing.T) {
	const old, target = `C:\Users\op\.no-mistakes`, `D:\CodeGoblins\no-mistakes`
	for _, test := range []struct {
		name, text, want string
	}{
		{name: "the home itself", text: `C:\Users\op\.no-mistakes`, want: `D:\CodeGoblins\no-mistakes`},
		{name: "a path below it", text: `C:\Users\op\.no-mistakes\worktrees\a1\r1`, want: `D:\CodeGoblins\no-mistakes\worktrees\a1\r1`},
		{name: "forward slashes", text: `gh_config_dir: 'C:/Users/op/.no-mistakes/forge-profiles/owner'`, want: `gh_config_dir: 'D:/CodeGoblins/no-mistakes/forge-profiles/owner'`},
		{name: "escaped as in JSON", text: `{"log":"C:\\Users\\op\\.no-mistakes\\logs\\r1.log"}`, want: `{"log":"D:\\CodeGoblins\\no-mistakes\\logs\\r1.log"}`},
		{name: "another case", text: `c:\users\OP\.No-Mistakes\repos\a1.git`, want: `D:\CodeGoblins\no-mistakes\repos\a1.git`},
		{name: "twice", text: `C:\Users\op\.no-mistakes\a;C:\Users\op\.no-mistakes\b`, want: `D:\CodeGoblins\no-mistakes\a;D:\CodeGoblins\no-mistakes\b`},
		{name: "a name that only begins with the home's", text: `C:\Users\op\.no-mistakes-backup\a`, want: `C:\Users\op\.no-mistakes-backup\a`},
		{name: "a file named after the home", text: `C:\Users\op\.no-mistakes.yaml`, want: `C:\Users\op\.no-mistakes.yaml`},
		{name: "nothing of the home", text: `C:\dev\code-goblins`, want: `C:\dev\code-goblins`},
		{name: "empty", text: ``, want: ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got := repoint(test.text, old, target)

			// Assert
			if got != test.want {
				t.Errorf("repoint(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}
