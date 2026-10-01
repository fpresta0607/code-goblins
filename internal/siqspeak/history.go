package siqspeak

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const historyBytes = 1 << 20
const historyEntries = 5
const maxTextBytes = 60 << 10

type Entry struct {
	Text      string  `json:"text"`
	Timestamp string  `json:"timestamp"`
	TimeEpoch float64 `json:"time_epoch"`
}

type History struct {
	Entries    []Entry `json:"entries"`
	HasSkipped bool    `json:"has_skipped"`
}

func readHistory(filename string) (History, error) {
	history := History{Entries: []Entry{}}
	file, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) {
		return history, nil
	}
	if err != nil {
		return history, errors.New("SIQspeak history could not be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return history, errors.New("SIQspeak history could not be read")
	}
	start := max(int64(0), info.Size()-historyBytes)
	data, err := io.ReadAll(io.NewSectionReader(file, start, historyBytes))
	if err != nil {
		return history, errors.New("SIQspeak history could not be read")
	}
	// A bounded tail may start in the middle of a UTF-8 character or JSON line.
	if start > 0 {
		_, data, _ = bytes.Cut(data, []byte{'\n'})
		history.HasSkipped = true
	}
	for len(data) > 0 && len(history.Entries) < historyEntries {
		index := bytes.LastIndexByte(data, '\n')
		line := bytes.TrimSpace(data[index+1:])
		data = data[:max(0, index)]
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if !utf8.Valid(line) || json.Unmarshal(line, &entry) != nil || strings.TrimSpace(entry.Text) == "" || len(entry.Text) > maxTextBytes {
			history.HasSkipped = true
			continue
		}
		history.Entries = append(history.Entries, entry)
	}
	return history, nil
}
