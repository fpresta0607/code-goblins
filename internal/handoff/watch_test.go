package handoff

import (
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

func cloudInto(base, url, head, login string) OpenPullRequest {
	return OpenPullRequest{URL: url, Title: "Name the bad header", Base: base, Head: head, Author: login}
}

func TestSightRecordsEachPullRequestIntoTheBranchAtItsNewestHead(t *testing.T) {
	const cloud = "https://github.com/" + testRepository + "/pull/7"
	at := time.Date(2026, 11, 25, 19, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		before    []CloudSighting
		open      []OpenPullRequest
		wantNew   bool
		wantCloud []CloudSighting
	}{
		{name: "a new pull request into the branch", open: []OpenPullRequest{cloudInto(testBranch, cloud, "head-1", testAccount)}, wantNew: true,
			wantCloud: []CloudSighting{{PullRequest: cloud, Title: "Name the bad header", Author: testAccount, Head: "head-1", SeenAt: at}}},
		{name: "a teammate's pull request", open: []OpenPullRequest{cloudInto(testBranch, cloud, "head-1", "teammate")}, wantNew: true,
			wantCloud: []CloudSighting{{PullRequest: cloud, Title: "Name the bad header", Author: "teammate", Head: "head-1", IsTeammates: true, SeenAt: at}}},
		{name: "the same head left alone stays left", before: []CloudSighting{{PullRequest: cloud, Title: "Name the bad header", Author: testAccount, Head: "head-1", IsLeft: true}}, open: []OpenPullRequest{cloudInto(testBranch, cloud, "head-1", testAccount)},
			wantCloud: []CloudSighting{{PullRequest: cloud, Title: "Name the bad header", Author: testAccount, Head: "head-1", IsLeft: true}}},
		{name: "a new head of a pull request left alone is new again", before: []CloudSighting{{PullRequest: cloud, Title: "Name the bad header", Author: testAccount, Head: "head-1", IsLeft: true}}, open: []OpenPullRequest{cloudInto(testBranch, cloud, "head-2", testAccount)}, wantNew: true,
			wantCloud: []CloudSighting{{PullRequest: cloud, Title: "Name the bad header", Author: testAccount, Head: "head-2", SeenAt: at}}},
		{name: "a pull request no longer open is dropped", before: []CloudSighting{{PullRequest: cloud, Title: "Name the bad header", Author: testAccount, Head: "head-1"}}},
		{name: "pull requests into another branch, from a fork or the handoff's own are not the cloud's", open: []OpenPullRequest{
			cloudInto("main", cloud, "head-1", testAccount),
			{URL: cloud, Base: testBranch, Head: "head-1", Author: testAccount, IsCrossRepository: true},
			cloudInto(testBranch, "", "head-1", testAccount),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f, record := handedOff(t)
			record.Cloud = test.before
			if err := WriteRecord(f.service.StateDir, record); err != nil {
				t.Fatal(err)
			}
			for index := range test.open {
				if test.open[index].URL == "" {
					test.open[index].URL = record.PullRequest
				}
			}

			// Act
			isNew, err := Sight(f.service.StateDir, f.meta, test.open, testAccount, at)

			// Assert
			sighted, readErr := ReadRecord(f.service.StateDir, f.meta.ID)
			if err != nil || readErr != nil || isNew != test.wantNew || !sameSightings(sighted.Cloud, test.wantCloud) {
				t.Fatalf("Sight = %v, %v; record %+v (%v); want new %v and %+v", isNew, err, sighted.Cloud, readErr, test.wantNew, test.wantCloud)
			}
		})
	}
}

func sameSightings(got, want []CloudSighting) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestSightLeavesATaskThatIsNotHandedOffInItsSessionAlone(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(*testing.T, *fixture)
	}{
		{name: "a goblin of a later session runs", arrange: func(t *testing.T, f *fixture) { f.newSession(t) }},
		{name: "a command holds the handoff's lock", arrange: func(t *testing.T, f *fixture) {
			name := ".handoff-" + f.meta.ID + ".lock"
			if _, err := lock.AcquireExclusiveNamed(f.service.StateDir, name); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(f.service.StateDir, name) })
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f, record := handedOff(t)
			test.arrange(t, f)

			// Act
			isNew, err := Sight(f.service.StateDir, f.meta, []OpenPullRequest{cloudInto(testBranch, "https://github.com/"+testRepository+"/pull/7", "head-1", testAccount)}, testAccount, time.Now())

			// Assert
			sighted, readErr := ReadRecord(f.service.StateDir, f.meta.ID)
			if err != nil || isNew || readErr != nil || len(sighted.Cloud) != 0 || sighted.PacketDigest != record.PacketDigest {
				t.Fatalf("Sight = %v, %v; record %+v (%v); want it left alone", isNew, err, sighted, readErr)
			}
		})
	}
}

func TestPressAndLeaveNameOnlyTheHeadTheBoardShows(t *testing.T) {
	const cloud = "https://github.com/" + testRepository + "/pull/7"
	for _, test := range []struct {
		name string
		act  func(f *fixture, head string) error
		want func(Record) bool
	}{
		{name: "the press", act: func(f *fixture, head string) error {
			return RecordPress(f.service.StateDir, f.meta, cloud, head, time.Now())
		},
			want: func(record Record) bool {
				return record.Press != nil && record.Press.PullRequest == cloud && record.Press.Head == "head-1"
			}},
		{name: "leave it", act: func(f *fixture, head string) error { return Leave(f.service.StateDir, f.meta, cloud, head) },
			want: func(record Record) bool { return len(record.Cloud) == 1 && record.Cloud[0].IsLeft }},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f, _ := handedOff(t)
			if _, err := Sight(f.service.StateDir, f.meta, []OpenPullRequest{cloudInto(testBranch, cloud, "head-1", "teammate")}, testAccount, time.Now()); err != nil {
				t.Fatal(err)
			}

			// Act
			movedErr := test.act(f, "head-0")
			err := test.act(f, "head-1")

			// Assert
			record, readErr := ReadRecord(f.service.StateDir, f.meta.ID)
			if movedErr == nil || !strings.Contains(movedErr.Error(), "head moved") || err != nil || readErr != nil || !test.want(record) {
				t.Fatalf("on an old head %v, on the shown head %v; record %+v (%v)", movedErr, err, record, readErr)
			}
		})
	}
}

// Leave it takes back the Overlord's press on that pull request, so no
// return takes a press he withdrew, and keeps one on another; a pull
// request he left is pressed again only once its head moves.
func TestLeaveTakesBackThePressOnThatPullRequest(t *testing.T) {
	const cloud = "https://github.com/" + testRepository + "/pull/7"
	const other = "https://github.com/" + testRepository + "/pull/8"
	for _, test := range []struct {
		name               string
		pressed, pressHead string
		wantPress          bool
	}{
		{name: "pressed on it", pressed: cloud, pressHead: "head-1"},
		{name: "pressed on another", pressed: other, pressHead: "head-8", wantPress: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f, _ := handedOff(t)
			open := []OpenPullRequest{cloudInto(testBranch, cloud, "head-1", "teammate"), cloudInto(testBranch, other, "head-8", "teammate")}
			if _, err := Sight(f.service.StateDir, f.meta, open, testAccount, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := RecordPress(f.service.StateDir, f.meta, test.pressed, test.pressHead, time.Now()); err != nil {
				t.Fatal(err)
			}

			// Act
			err := Leave(f.service.StateDir, f.meta, cloud, "head-1")
			checkErr := CheckSighting(f.service.StateDir, f.meta, cloud, "head-1")
			pressErr := RecordPress(f.service.StateDir, f.meta, cloud, "head-1", time.Now())

			// Assert
			record, readErr := ReadRecord(f.service.StateDir, f.meta.ID)
			if err != nil || readErr != nil || (record.Press != nil) != test.wantPress || test.wantPress && record.Press.PullRequest != other {
				t.Fatalf("leave = %v, record %+v (%v); want a press %v", err, record, readErr, test.wantPress)
			}
			if checkErr == nil || pressErr == nil || !strings.Contains(pressErr.Error(), "was left") {
				t.Fatalf("on the left pull request: check %v, press %v; want both refused", checkErr, pressErr)
			}
		})
	}
}

// A teammate's pull request is brought back only with the Overlord's press
// on it at the head GitHub reports now, so a push after the press needs a
// new one.
func TestReturnBringsBackATeammatesPullRequestOnlyWithThePressAtItsHead(t *testing.T) {
	for _, test := range []struct {
		name      string
		author    string
		pressHead func(cloud pullRequest) string
		wantErr   string
	}{
		{name: "no press", author: "teammate", wantErr: "needs the Overlord's press"},
		{name: "a press on an older head", author: "teammate", pressHead: func(pullRequest) string { return "head-before-the-push" }, wantErr: "needs the Overlord's press"},
		{name: "a press at its head", author: "teammate", pressHead: func(cloud pullRequest) string { return cloud.HeadRefOid }},
		{name: "the account's own, pushed after the press", author: testAccount, pressHead: func(pullRequest) string { return "head-before-the-push" }, wantErr: "needs the Overlord's press"},
		{name: "the account's own, with no press", author: testAccount},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f, record := handedOff(t)
			cloud := f.openCloudPullRequest(t, record.Head, "errors.go", "package parser\n", test.author)
			if test.pressHead != nil {
				record.Press = &Press{PullRequest: cloud.URL, Head: test.pressHead(cloud), At: time.Now().UTC()}
				if err := WriteRecord(f.service.StateDir, record); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			returned, err := f.service.Return(t.Context(), f.meta.ID, cloud.URL)

			// Assert
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) || f.remoteHead(t, testBranch) != record.Head || !returned.IsHandedOff() {
					t.Fatalf("return = %v, record %+v; want %q with nothing pushed", err, returned, test.wantErr)
				}
				return
			}
			if err != nil || returned.Outcome != OutcomeReturned || f.remoteHead(t, testBranch) != cloud.HeadRefOid {
				t.Fatalf("return = %v, record %+v; want %s brought back", err, returned, cloud.HeadRefOid)
			}
		})
	}
}
