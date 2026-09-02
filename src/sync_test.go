package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name           string
		args           []string
		wantPositional []string
		wantFlagArgs   []string
	}{
		{
			// The exact shape that broke: folder path first, flags after —
			// Go's flag package alone would treat all of this as positional.
			name:           "documented order: folder path first",
			args:           []string{"./apps/cards", "-a", "1", "-h", "192.168.18.4:8787"},
			wantPositional: []string{"./apps/cards"},
			wantFlagArgs:   []string{"-a", "1", "-h", "192.168.18.4:8787"},
		},
		{
			name:           "flags first, folder path last",
			args:           []string{"-a", "1", "-h", "192.168.18.4:8787", "./apps/cards"},
			wantPositional: []string{"./apps/cards"},
			wantFlagArgs:   []string{"-a", "1", "-h", "192.168.18.4:8787"},
		},
		{
			name:           "folder path sandwiched between flags",
			args:           []string{"-a", "1", "./apps/cards", "-h", "192.168.18.4:8787"},
			wantPositional: []string{"./apps/cards"},
			wantFlagArgs:   []string{"-a", "1", "-h", "192.168.18.4:8787"},
		},
		{
			name:           "-flag=value form",
			args:           []string{"./apps/cards", "-a=1", "-h=192.168.18.4:8787"},
			wantPositional: []string{"./apps/cards"},
			wantFlagArgs:   []string{"-a=1", "-h=192.168.18.4:8787"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			positional, flagArgs := splitArgs(c.args, flagsWithValue)
			if !reflect.DeepEqual(positional, c.wantPositional) {
				t.Errorf("positional = %v, want %v", positional, c.wantPositional)
			}
			if !reflect.DeepEqual(flagArgs, c.wantFlagArgs) {
				t.Errorf("flagArgs = %v, want %v", flagArgs, c.wantFlagArgs)
			}
		})
	}
}

func TestPromptSourceOfTruth(t *testing.T) {
	cases := []struct {
		input string
		want  source
	}{
		{"a\n", sourceApp},
		{"app\n", sourceApp},
		{"A\n", sourceApp},
		{"l\n", sourceLocal},
		{"local\n", sourceLocal},
		{"  L  \n", sourceLocal},
	}
	for _, c := range cases {
		got, err := promptSourceOfTruth(strings.NewReader(c.input), &bytes.Buffer{})
		if err != nil {
			t.Fatalf("promptSourceOfTruth(%q): %v", c.input, err)
		}
		if got != c.want {
			t.Errorf("promptSourceOfTruth(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestPromptSourceOfTruthRepromptsOnGarbage(t *testing.T) {
	var out bytes.Buffer
	got, err := promptSourceOfTruth(strings.NewReader("nonsense\nl\n"), &out)
	if err != nil {
		t.Fatalf("promptSourceOfTruth: %v", err)
	}
	if got != sourceLocal {
		t.Errorf("got %v, want sourceLocal", got)
	}
	if !strings.Contains(out.String(), `please answer`) {
		t.Errorf("expected a reprompt message, got %q", out.String())
	}
}

func TestWatchStdinForReloadSignalsOnR(t *testing.T) {
	// Lowercase and uppercase should both count, and it fires on the byte
	// itself — no line/Enter needed, matching runSync's raw terminal mode.
	reload, _ := watchStdinForReload(strings.NewReader("R"))
	select {
	case <-reload:
	case <-time.After(time.Second):
		t.Fatal("expected a reload signal, got none")
	}
}

func TestWatchStdinForReloadIgnoresOtherBytes(t *testing.T) {
	reload, _ := watchStdinForReload(strings.NewReader("hello, please wait"))
	select {
	case <-reload:
		t.Fatal("unexpected reload signal: input contains no r/R byte")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWatchStdinForReloadFiresMidWord(t *testing.T) {
	// Every byte is live the instant it arrives (raw mode, no line
	// buffering) — "world" contains an 'r', so it fires too, unlike the old
	// line-based "whole line must equal r" behavior.
	reload, _ := watchStdinForReload(strings.NewReader("world"))
	select {
	case <-reload:
	case <-time.After(time.Second):
		t.Fatal("expected a reload signal from the r in \"world\"")
	}
}

func TestWatchStdinForReloadDoesNotQueueMultipleSignals(t *testing.T) {
	reload, _ := watchStdinForReload(strings.NewReader("rrr"))
	time.Sleep(100 * time.Millisecond) // let the goroutine drain all three bytes

	select {
	case <-reload:
	default:
		t.Fatal("expected at least one pending reload signal")
	}
	select {
	case <-reload:
		t.Fatal("expected only one buffered signal (repeated R shouldn't queue up)")
	default:
	}
}

func TestWatchStdinForReloadSignalsInterruptOnCtrlC(t *testing.T) {
	_, interrupt := watchStdinForReload(strings.NewReader("\x03"))
	select {
	case <-interrupt:
	case <-time.After(time.Second):
		t.Fatal("expected an interrupt signal from Ctrl+C (0x03), got none")
	}
}
