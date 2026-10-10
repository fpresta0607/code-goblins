package supervisor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/defender"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// defenderEvery is how often the supervisor reads Microsoft Defender's
// records. A read takes about four seconds.
const defenderEvery = 10 * time.Minute

// defenderOverlap is how far back of the last read the next one starts:
// Defender dates a detection by when it first saw the file, which can be
// before the record of it appears.
const defenderOverlap = time.Hour

// checkDefender reads what Microsoft Defender recorded under the fleet's
// folders, every defenderEvery, and raises one wake to the CFO for each
// detection and each sample sent to Microsoft that it has not told the CFO
// of: the file, and the test and task that made it. The Overlord otherwise
// meets each as a Windows notice with nobody to say what the fleet did. It
// only reads, and nothing of Defender's is changed. The first reading only
// marks where the watch begins, so what Defender recorded before raises
// nothing.
func (s *Service) checkDefender(ctx context.Context, w *fleetWakes, now time.Time) error {
	read := s.Options.Defender
	if read == nil {
		return nil
	}
	if w.DefenderSince.IsZero() {
		w.DefenderSince = now
		w.woke("defender", now)
		return nil
	}
	if !w.due("defender", defenderEvery, now) {
		return nil
	}
	w.woke("defender", now)
	recorded, err := read(ctx, w.DefenderSince)
	if err := w.failing("defender", err); err != nil {
		return err
	}
	if err != nil {
		return nil
	}
	h := s.Store.Home
	report := recorded.Under(defender.Folders(h.Root, h.DevDrive))
	stamp := w.DefenderSince.UTC().Format(time.RFC3339)
	for _, detection := range report.Detections {
		if detection.Time.Before(w.DefenderSince) {
			continue
		}
		what := "command lines and no file"
		if len(detection.Files) > 0 {
			what = strings.Join(madeBy(detection.Files), ", ")
		}
		detail := fmt.Sprintf("defender_detection: Microsoft Defender detected %s at %s on %s. Nothing of Defender's is changed, and a quarantined file is never restored. Read it with cfo defender --since %s, find what the fleet did there and change that.", detection.Threat, detection.Time.UTC().Format(time.RFC3339), what, stamp)
		if err := raiseDefenderWake(h.State, "defender/detection/"+detection.ID, detail); err != nil {
			return err
		}
	}
	for _, upload := range report.Uploads {
		if upload.Time.Before(w.DefenderSince) {
			continue
		}
		detail := fmt.Sprintf("defender_upload: Microsoft Defender sent %s to Microsoft at %s, SHA-256 %s. It sends a program or a script nobody has seen, so each new unsigned build and each edited install script is one more. Read it with cfo defender --since %s.", madeBy([]string{upload.File})[0], upload.Time.UTC().Format(time.RFC3339), upload.SHA256[:min(12, len(upload.SHA256))], stamp)
		if err := raiseDefenderWake(h.State, "defender/upload/"+upload.SHA256+"/"+upload.Time.UTC().Format(time.RFC3339), detail); err != nil {
			return err
		}
	}
	if next := now.Add(-defenderOverlap); next.After(w.DefenderSince) {
		w.DefenderSince = next
	}
	return nil
}

// madeBy is each file's path and, where its folders say, who made it.
func madeBy(files []string) []string {
	said := make([]string, 0, len(files))
	for _, file := range files {
		if origin := defender.OriginOf(file).String(); origin != "" {
			file += " (" + origin + ")"
		}
		said = append(said, file)
	}
	return said
}

// raiseDefenderWake wakes the CFO with detail once for identity, however
// often the record behind it is read.
func raiseDefenderWake(stateDir, identity, detail string) error {
	_, isNew, err := wake.AppendFirst(stateDir, identity, "check", "defender", detail)
	if err != nil || !isNew {
		return err
	}
	_, err = wake.PublishEpisode(stateDir)
	return err
}
