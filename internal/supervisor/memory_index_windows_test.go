package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/memory"
)

func writeMemoryFact(t *testing.T, dataDir, name, description string) {
	t.Helper()
	path := filepath.Join(dataDir, "memory", name+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: "+description+"\n---\n"+description+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryIndexAnswersFromItsBuildAndRebuildsWhenASourceChanges(t *testing.T) {
	_, h := testStore(t)
	var keeper memoryIndex
	question := memory.RecallQuery{Question: "harbor database", Limit: 5}
	if _, err := keeper.recall(question); err == nil || !strings.Contains(err.Error(), "still being built") {
		t.Fatalf("an unbuilt index answered: %v", err)
	}
	writeMemoryFact(t, h.Data, "harbor-database", "harbor has no hosted database")

	keeper.refresh(h.Data, h.State)
	first, err := keeper.recall(question)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "harbor-database" {
		t.Fatalf("first answer %+v: %v", first, err)
	}
	keeper.refresh(h.Data, h.State)
	if again, err := keeper.recall(question); err != nil || !again.BuiltAt.Equal(first.BuiltAt) {
		t.Fatalf("an unchanged index was rebuilt: %v", err)
	}
	writeMemoryFact(t, h.Data, "harbor-database-live", "the harbor hosted database is live")
	keeper.refresh(h.Data, h.State)
	changed, err := keeper.recall(question)

	if err != nil || len(changed.Items) != 2 {
		t.Fatalf("the new fact was not indexed: %+v %v", changed, err)
	}
}

func TestMemoryIndexReportsAFailedRebuildInsteadOfAnsweringFromTheOldOne(t *testing.T) {
	_, h := testStore(t)
	writeMemoryFact(t, h.Data, "harbor-database", "harbor has no hosted database")
	var keeper memoryIndex
	keeper.refresh(h.Data, h.State)
	if err := os.Mkdir(filepath.Join(h.Data, "overlord.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	keeper.refresh(h.Data, h.State)
	_, err := keeper.recall(memory.RecallQuery{Question: "harbor database", Limit: 5})

	if err == nil || !strings.Contains(err.Error(), "the memory index could not be rebuilt") {
		t.Fatalf("a failed rebuild answered: %v", err)
	}
}

func TestRecallWithNoSupervisorSaysSoAtOnce(t *testing.T) {
	_, h := testStore(t)
	started := time.Now()

	_, err := Recall(h.State, memory.RecallQuery{Question: "harbor", Limit: 5})

	if err != ErrNotRunning || time.Since(started) > time.Second {
		t.Fatalf("recall with no supervisor gave %v after %s", err, time.Since(started))
	}
}

func TestARecallRequestIsAnsweredOverThePipe(t *testing.T) {
	store, h := testStore(t)
	writeMemoryFact(t, h.Data, "harbor-database", "harbor has no hosted database")
	s := &Service{Store: store, looks: make(chan chan struct{})}
	s.memory.refresh(h.Data, h.State)
	client, _ := replyPipe(t, s, context.Background())
	request, err := json.Marshal(runPipeRequest{Kind: "recall", Recall: &memory.RecallQuery{Question: "hosted database", Limit: 5}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Write(append(request, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(client).ReadBytes('\n')
	var reply pipeReply

	if err != nil || json.Unmarshal(line, &reply) != nil || reply.Error != "" || reply.Recall == nil || len(reply.Recall.Items) != 1 || reply.Recall.Items[0].ID != "harbor-database" || reply.Recall.Items[0].Excerpt != "harbor has no hosted database" {
		t.Fatalf("reply %s: %v", line, err)
	}
	if strings.Contains(string(line), "\"Text\"") {
		t.Fatalf("the reply carried whole item text: %s", line)
	}
}
