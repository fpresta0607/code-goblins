package handoff

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

func TestThePacketIsScreenedAsAPublicIssueOnEveryRepository(t *testing.T) {
	screen := tickets.Screen{Secrets: []string{"s3cr3t-value-1234"}, People: []string{"Pat Teammate"}, Private: []string{"secret-project"}, Account: "acme"}
	for _, test := range []struct {
		name, line, want string
	}{
		{name: "a credential value", line: "token s3cr3t-value-1234 here", want: "credential value"},
		{name: "a private key", line: "-----BEGIN OPENSSH PRIVATE KEY----- abc", want: "private key"},
		{name: "a key-shaped word", line: "key ghp_" + strings.Repeat("A1", 18), want: "shape of a key"},
		{name: "a path on this machine", line: `left a log at C:\Users\someone\log.txt`, want: "path on this machine"},
		{name: "a teammate's name", line: "Pat Teammate reviewed it", want: "a teammate"},
		{name: "a mention other than the owner", line: "ask @someone-else", want: "mentions @someone-else"},
		{name: "a private project's name", line: "seen first in secret-project", want: "names secret-project"},
	} {
		for _, isPublic := range []bool{false, true} {
			t.Run(test.name+map[bool]string{false: " on a private repository", true: " on a public one"}[isPublic], func(t *testing.T) {
				// Arrange
				screen := screen
				screen.IsPublic = isPublic
				text := "first line\n" + test.line + "\nlast line"

				// Act
				refusal := Refusal(screen, text)

				// Assert
				if !strings.Contains(refusal, "line 2") || !strings.Contains(refusal, test.want) {
					t.Fatalf("refusal = %q, want line 2 refused for %q", refusal, test.want)
				}
				if strings.Contains(refusal, "s3cr3t-value-1234") {
					t.Fatalf("refusal %q repeats the secret", refusal)
				}
			})
		}
	}
}

func TestAPacketWithNothingToRefuseIsPublished(t *testing.T) {
	// Arrange
	packet := Packet{Handoff: "The parser is done; its tests are left.", Branch: "feat/parser", Head: strings.Repeat("a", 40), DefaultBranch: "main", Commits: []string{"abc1234 feat(parser): read the header"}, Mode: "no-mistakes", Commands: []Command{{Text: "go test ./internal/parser"}}}

	// Act
	refusal := Refusal(tickets.Screen{Account: "acme"}, packet.Text())

	// Assert
	if refusal != "" {
		t.Fatalf("refusal = %q, want none", refusal)
	}
}

func TestVerificationCommandsSayWhatACloudSessionCannotRun(t *testing.T) {
	// Arrange
	verification := project.Verification{
		Fast: []project.Command{{"go", "test", "./internal/handoff"}, {"go", "test", "./cmd/goblins"}},
		Full: []project.Command{{"go", "test", "./..."}, {"goblins", "gate", "test"}, {"go", "test", "./internal/..."}, {"go", "test", "./cmd/..."}, {"go", "test", "./internal/handoff/..."}, {"go", "test", "./internal/super/..."}},
	}

	// Act
	commands := VerificationCommands(verification, []string{"./cmd/goblins", "./internal/supervisor"})

	// Assert
	want := []Command{
		{Text: "go test ./internal/handoff"},
		{Text: "go test ./cmd/goblins", IsWindowsOnly: true},
		{Text: "go test ./...", IsWindowsOnly: true},
		{Text: "goblins gate test", NeedsGoblins: true},
		{Text: "go test ./internal/...", IsWindowsOnly: true},
		{Text: "go test ./cmd/...", IsWindowsOnly: true},
		{Text: "go test ./internal/handoff/..."},
		{Text: "go test ./internal/super/..."},
	}
	if !slices.Equal(commands, want) {
		t.Fatalf("commands = %+v, want %+v", commands, want)
	}
}

func TestLinuxExceptReadsTheLinuxJobsListFromThisRepositorysWorkflow(t *testing.T) {
	// Arrange
	workflow, err := os.ReadFile(filepath.Join("..", "..", workflowPath))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	except := LinuxExcept(string(workflow))

	// Assert
	if !slices.Contains(except, "./cmd/goblins") || !slices.Contains(except, "./internal/supervisor") || slices.Contains(except, "./internal/handoff") {
		t.Fatalf("except = %v, want ./cmd/goblins and ./internal/supervisor, and never ./internal/handoff, whose tests run on Linux", except)
	}
}

func TestSpliceWritesOverItsOwnPacketAndKeepsTheRestOfTheBody(t *testing.T) {
	// Arrange
	first := PacketStart + "\nfirst\n" + PacketEnd
	second := PacketStart + "\nsecond\n" + PacketEnd
	body := "## What Changed\n\nThe parser.\n"

	// Act
	once := Splice(body, first)
	twice := Splice(once, second)

	// Assert
	if !strings.HasPrefix(twice, body) || strings.Contains(twice, "first") || strings.Count(twice, PacketStart) != 1 || !strings.Contains(twice, "second") {
		t.Fatalf("body after two packets = %q", twice)
	}
}

func TestMarkCancelledMarksThePacketOnce(t *testing.T) {
	// Arrange
	body := Splice("Intro.", PacketStart+"\npacket\n"+PacketEnd)

	// Act
	marked := MarkCancelled(MarkCancelled(body))

	// Assert
	if strings.Count(marked, cancelledLine) != 1 || !strings.HasPrefix(marked, "Intro.") {
		t.Fatalf("marked body = %q", marked)
	}
}

// The packet's own markers inside its content would end it there, so the
// next splice or cancel would leave its tail outside it in the body.
func TestAPacketWhoseContentHoldsItsOwnMarkerIsRefused(t *testing.T) {
	for _, test := range []struct {
		name   string
		packet Packet
	}{
		{name: "a handoff naming the end marker", packet: Packet{Handoff: "The packet sits before " + PacketEnd + " now."}},
		{name: "a handoff naming the start marker", packet: Packet{Handoff: "It starts at " + PacketStart + "."}},
		{name: "a commit subject naming the end marker", packet: Packet{Handoff: "Done.", Commits: []string{"abc1234 docs: name " + PacketEnd}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			test.packet.Branch, test.packet.Head, test.packet.DefaultBranch = "feat/parser", strings.Repeat("a", 40), "main"

			// Act
			refusal := Refusal(tickets.Screen{Account: "acme"}, test.packet.Text())

			// Assert
			if !strings.Contains(refusal, "marker") {
				t.Fatalf("refusal = %q, want the packet refused for holding its own marker", refusal)
			}
		})
	}
}

// A handoff line that shares eight words in a row with a line of the task's
// private texts copies it; seven in a row, or the same words apart, do not.
// Case and punctuation do not hide a copy.
func TestAHandoffLineCopyingEightWordsOfAPrivateTextIsRefused(t *testing.T) {
	private := []string{"Intro line.\nThe parser must reject a header longer than the buffer it reads."}
	for _, test := range []struct {
		name      string
		handoff   string
		isRefused bool
	}{
		{name: "eight words in a row", handoff: "Done.\nNote: the parser must reject a header longer than the limit.", isRefused: true},
		{name: "in another case and punctuation", handoff: "THE PARSER, MUST reject; a header longer -- than", isRefused: true},
		{name: "seven words in a row", handoff: "the parser must reject a header longer, and more"},
		{name: "the same words apart", handoff: "the parser must reject\na header longer than the buffer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			refusal := CopyRefusal(test.handoff, private)

			// Assert
			if isRefused := refusal != ""; isRefused != test.isRefused {
				t.Fatalf("CopyRefusal = %q, want refused %v", refusal, test.isRefused)
			}
			if test.isRefused && strings.Contains(refusal, "parser") {
				t.Errorf("CopyRefusal = %q repeats what it found", refusal)
			}
		})
	}
}
