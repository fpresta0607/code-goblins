package spawn

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A task that asked for direct-PR with no change assessment starts strict, so
// its goblin's first message names no-mistakes as its frozen mode, puts it
// over the brief's mode line and Verification section, and gives the checks
// to the pipeline alone, whatever the brief it was started with says.
func TestAStrictStartOfADirectPRRequestNamesItsOneValidationOwner(t *testing.T) {
	for _, test := range []struct{ name, brief string }{
		{"queued row", fleet.QueuedBriefText(fleet.QueuedTask{Row: fleet.BacklogRow{ID: "task-7", Repo: "northwind", Title: "Say why a billing sync fails", Mode: "direct-PR"}})},
		{"pre-upgrade root brief", "# Brief task-7\n\n## Task\n\n> Fix the report.\n\n## Verification\n\nBefore each push run `cfo verify task-7`. Fix that before you push.\n\n## Delivery\n\nkind: ship\nmode: direct-PR\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			fixture := newQuickFixture(t)
			if err := os.WriteFile(fixture.brief, []byte(test.brief), 0o600); err != nil {
				t.Fatal(err)
			}
			selection := project.SelectDelivery(project.Manifest{}, test.brief, "direct-PR", "")
			fixture.request.Mode, fixture.request.Delivery = selection.Mode, &selection

			// Act
			_, err := fixture.service.Spawn(t.Context(), fixture.request)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			submitted := submittedLines(t, fixture, 1)
			if len(submitted) != 1 {
				t.Fatalf("submitted = %q, want the goblin's first message", submitted)
			}
			instruction := delivered(t, submitted[0])
			for _, want := range []string{"Your delivery mode is no-mistakes", "it supersedes the brief's mode line and Verification section", "Drive `cfo pipeline run task-7 --intent-file <file>`", "Do not start a separate cfo verify"} {
				if !strings.Contains(instruction, want) {
					t.Errorf("first message lacks %q:\n%s", want, instruction)
				}
			}
			if strings.Contains(instruction, "Before each push run") {
				t.Errorf("first message gives the checks a second owner:\n%s", instruction)
			}
		})
	}
}

func TestSpawnPersistsDeliveryBeforeTheWorkerStarts(t *testing.T) {
	// Arrange
	fixture := newQuickFixture(t)
	selection := project.SelectDelivery(project.Manifest{}, "", "no-mistakes", "")
	fixture.request.Delivery = &selection
	isReadBeforeLaunch := false
	fixture.request.BeforeLaunch = func() error {
		meta, err := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID)
		if err != nil {
			return err
		}
		frozen, err := state.ReadTaskDelivery(meta)
		if err != nil {
			return err
		}
		if frozen == nil || meta.DeliveryHash == "" || frozen.Mode != selection.Mode {
			return errors.New("delivery selection was not frozen before launch")
		}
		isReadBeforeLaunch = true
		return nil
	}

	// Act
	result, err := fixture.service.Spawn(t.Context(), fixture.request)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := state.ReadTaskDelivery(meta)
	if err != nil || !isReadBeforeLaunch || meta.DeliveryHash == "" || frozen == nil || frozen.Mode != "no-mistakes" || result.Meta.DeliveryHash != meta.DeliveryHash {
		t.Fatalf("meta=%+v selection=%+v error=%v", meta, frozen, err)
	}
}
