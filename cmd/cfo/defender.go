package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/defender"
)

// runDefender prints what Microsoft Defender recorded under the fleet's
// folders since a time: its detections and the samples it sent to Microsoft.
// It only reads Defender's own records, and changes nothing of Defender's.
func runDefender(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("defender", flag.ContinueOnError)
	f.SetOutput(stderr)
	since := f.String("since", "24h", "read from this time: an RFC 3339 time, a day such as 2026-10-10, or a length back from now such as 36h or 7d")
	asJSON := f.Bool("json", false, "print the report as JSON")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: cfo defender [--since <time|day|length>] [--json]")
		return 2
	}
	from, err := sinceTime(*since, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, "cfo defender: "+err.Error())
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	read, err := runtime.readDefender(context.Background(), from)
	if err != nil {
		fmt.Fprintln(stderr, "cfo defender: "+err.Error())
		return 1
	}
	report := read.Under(defender.Folders(h.Root, h.DevDrive))
	if *asJSON {
		return printDefenderJSON(stdout, stderr, from, report)
	}
	stamp := from.Format(time.RFC3339)
	if len(report.Detections) == 0 && len(report.Uploads) == 0 {
		fmt.Fprintln(stdout, "Microsoft Defender recorded no detection and no upload under the fleet's folders since "+stamp)
	} else {
		fmt.Fprintf(stdout, "Microsoft Defender's records under the fleet's folders since %s: %s, %s\n", stamp, counted(len(report.Detections), "detection"), counted(len(report.Uploads), "upload"))
	}
	for _, detection := range report.Detections {
		fmt.Fprintf(stdout, "detection %s %s\n", detection.Time.UTC().Format(time.RFC3339), detection.Threat)
		for _, file := range detection.Files {
			fmt.Fprintln(stdout, "  "+madeBy(file))
		}
		if len(detection.Files) == 0 {
			fmt.Fprintf(stdout, "  %s, no file\n", counted(detection.CommandLines, "command line"))
		}
	}
	for _, upload := range report.Uploads {
		fmt.Fprintf(stdout, "upload %s %s sha256 %s\n", upload.Time.UTC().Format(time.RFC3339), madeBy(upload.File), upload.SHA256[:min(12, len(upload.SHA256))])
	}
	if report.Elsewhere == 1 {
		fmt.Fprintln(stdout, "1 more that names no file under the fleet's folders is not listed")
	} else if report.Elsewhere > 1 {
		fmt.Fprintf(stdout, "%d more that name no file under the fleet's folders are not listed\n", report.Elsewhere)
	}
	return 0
}

// madeBy is a file's path and, where its folders say, who made it.
func madeBy(file string) string {
	if origin := defender.OriginOf(file).String(); origin != "" {
		return file + " (" + origin + ")"
	}
	return file
}

// counted is n things, as 1 detection or 2 detections.
func counted(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return strconv.Itoa(n) + " " + thing + "s"
}

// defenderFile is a file a record names, with what its path says of who
// made it.
type defenderFile struct {
	Path string `json:"path"`
	Task string `json:"task,omitempty"`
	Test string `json:"test,omitempty"`
	Gate string `json:"gate,omitempty"`
}

func fileOf(path string) defenderFile {
	origin := defender.OriginOf(path)
	return defenderFile{Path: path, Task: origin.Task, Test: origin.Test, Gate: origin.Gate}
}

func printDefenderJSON(stdout, stderr io.Writer, from time.Time, report defender.Report) int {
	type detection struct {
		ID           string         `json:"id"`
		Time         time.Time      `json:"time"`
		Threat       string         `json:"threat"`
		Process      string         `json:"process"`
		Files        []defenderFile `json:"files"`
		CommandLines int            `json:"command_lines"`
	}
	type upload struct {
		Time   time.Time    `json:"time"`
		File   defenderFile `json:"file"`
		SHA256 string       `json:"sha256"`
	}
	printed := struct {
		Since      time.Time   `json:"since"`
		Detections []detection `json:"detections"`
		Uploads    []upload    `json:"uploads"`
		Elsewhere  int         `json:"elsewhere"`
	}{Since: from, Detections: []detection{}, Uploads: []upload{}, Elsewhere: report.Elsewhere}
	for _, found := range report.Detections {
		files := []defenderFile{}
		for _, file := range found.Files {
			files = append(files, fileOf(file))
		}
		printed.Detections = append(printed.Detections, detection{ID: found.ID, Time: found.Time, Threat: found.Threat, Process: found.Process, Files: files, CommandLines: found.CommandLines})
	}
	for _, sent := range report.Uploads {
		printed.Uploads = append(printed.Uploads, upload{Time: sent.Time, File: fileOf(sent.File), SHA256: sent.SHA256})
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(printed); err != nil {
		fmt.Fprintln(stderr, "cfo defender: "+err.Error())
		return 1
	}
	return 0
}

// sinceTime reads the time --since names: an RFC 3339 time, a day, or a
// length back from now in hours, minutes or days.
func sinceTime(given string, now time.Time) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, given); err == nil {
		return at.UTC(), nil
	}
	if day, err := time.Parse(time.DateOnly, given); err == nil {
		return day, nil
	}
	length, err := time.ParseDuration(given)
	if days, isDays := strings.CutSuffix(given, "d"); isDays {
		var count int
		count, err = strconv.Atoi(days)
		length = time.Duration(count) * 24 * time.Hour
	}
	if err != nil || length <= 0 {
		return time.Time{}, errors.New("--since takes an RFC 3339 time such as 2026-10-10T15:00:00Z, a day such as 2026-10-10, or a length back from now such as 36h or 7d, not " + strconv.Quote(given))
	}
	return now.Add(-length), nil
}
