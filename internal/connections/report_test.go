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
	if mode == "slow-ready" {
		time.Sleep(2 * time.Second)
	}
	fmt.Println("started")
	if mode != "hang" {
		fmt.Println("ready")
	}
	var input [1]byte
	_, _ = os.Stdin.Read(input[:])
	os.Exit(0)
}

func TestHarnessReportClosesItsOwnProcessAndBoundsAHangingCheck(t *testing.T) {
	for _, mode := range []string{"ready", "slow-ready", "hang"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			hasStarted := false
			entries, err := harnessReport(ctx, os.Args[0], []string{"-test.run=^TestConnectionReportChild$"}, t.TempDir(), append(os.Environ(), "CONNECTION_REPORT_FIXTURE="+mode, "CLAUDECODE=parent"), func(output io.Reader, _ io.Writer) ([]Entry, error) {
				reader := bufio.NewReader(output)
				line, err := reader.ReadString('\n')
				if err != nil || line != "started\n" {
					return nil, errors.New("fixture did not start")
				}
				hasStarted = true
				if mode == "hang" {
					cancel()
				}
				line, err = reader.ReadString('\n')
				if err != nil {
					return nil, errors.New("check stopped")
				}
				if line != "ready\n" {
					return nil, errors.New("unexpected report")
				}
				return []Entry{{Name: "fixture", Status: "connected"}}, nil
			})
			if !hasStarted {
				t.Fatalf("fixture did not start: %v", err)
			}
			if mode != "hang" && (err != nil || len(entries) != 1) {
				t.Fatalf("report failed: %v", err)
			}
			if mode == "hang" && (err == nil || !errors.Is(ctx.Err(), context.Canceled)) {
				t.Fatalf("hanging check did not stop on cancellation: %v, context: %v", err, ctx.Err())
			}
			if mode != "hang" && ctx.Err() != nil {
				t.Fatalf("ready check did not finish before its deadline: %v", ctx.Err())
			}
		})
	}
}
