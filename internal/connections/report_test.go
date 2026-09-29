package connections

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

func TestConnectionReportChild(t *testing.T) {
	mode := os.Getenv("CONNECTION_REPORT_FIXTURE")
	if mode == "" {
		return
	}
	if os.Getenv("CLAUDECODE") != "" {
		os.Exit(3)
	}
	if mode == "ready" {
		fmt.Println("ready")
	}
	var input [1]byte
	_, _ = os.Stdin.Read(input[:])
	os.Exit(0)
}

func TestHarnessReportClosesItsOwnProcessAndBoundsAHangingCheck(t *testing.T) {
	for _, mode := range []string{"ready", "hang"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			started := time.Now()
			entries, err := harnessReport(ctx, os.Args[0], []string{"-test.run=^TestConnectionReportChild$"}, t.TempDir(), append(os.Environ(), "CONNECTION_REPORT_FIXTURE="+mode, "CLAUDECODE=parent"), func(output io.Reader, _ io.Writer) ([]Entry, error) {
				line, err := bufio.NewReader(output).ReadString('\n')
				if err != nil {
					return nil, errors.New("check stopped")
				}
				if line != "ready\n" {
					return nil, errors.New("unexpected report")
				}
				return []Entry{{Name: "fixture", Status: "connected"}}, nil
			})
			if mode == "ready" && (err != nil || len(entries) != 1) {
				t.Fatalf("report failed: %v", err)
			}
			if mode == "hang" && err == nil {
				t.Fatal("hanging check succeeded")
			}
			if time.Since(started) > 5*time.Second {
				t.Fatal("subprocess exceeded its deadline")
			}
		})
	}
}
