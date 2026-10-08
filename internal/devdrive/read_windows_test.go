package devdrive

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Read works without administrator rights, which `cfo doctor` and the board
// have: it names this Windows, the system drive and its file system, and never
// takes a drive that is not ReFS for a Dev Drive.
func TestReadReadsThisMachineWithoutAdministratorRights(t *testing.T) {
	m, err := Read(context.Background(), execx.OSRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Build < 10240 || m.SystemFree == 0 || m.SystemDrive == "" {
		t.Errorf("machine = %+v, want this Windows' build and the system drive's free space", m)
	}
	system, found := m.VolumeOf(os.Getenv("SystemDrive") + `\Windows`)
	if !found || system.FileSystem == "" || system.Total == 0 {
		t.Errorf("the system drive is not among %+v", m.Volumes)
	}
	for _, v := range m.Volumes {
		if v.Dev && !strings.EqualFold(v.FileSystem, "ReFS") {
			t.Errorf("%s is %s and was taken for a Dev Drive", v.Root, v.FileSystem)
		}
	}
	if m.Defender && m.DefenderUnread != "" {
		t.Errorf("Defender read as on with a reason it could not be read: %q", m.DefenderUnread)
	}
}
