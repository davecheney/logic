package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davecheney/logic"
)

func TestInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-device", "saleae"},
		{"-format", "xml"}, {"-channels", "256"}, {"-channels", "0"},
		{"-rate", "NaN"}, {"-rate", "16000000"}, {"-samples", "16000"},
		{"-trigger", "4"}, {"-wait", "-1s"}, {"unexpected"},
		{"-preset", "short"}, {"-preset", "long"}, {"-preset", "long", "-device", "bitscope"},
		{"-preset", "long", "-device", "dslogic", "-channels", "0xffff", "-samples", "20000000"},
	} {
		if len(args) > 1 && args[0] != "-device" && args[0] != "-preset" {
			args = append([]string{"-device", "bitscope"}, args...)
		}
		var out, stderr bytes.Buffer
		if err := run(args, &out, &stderr); err == nil || out.Len() != 0 {
			t.Fatalf("args %v: invalid options accepted or wrote output", args)
		}
	}
}

func TestDeviceRequired(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, &out, &out); err == nil || !strings.Contains(err.Error(), "-device is required") {
		t.Fatalf("no device: %v", err)
	}
}

func TestHelp(t *testing.T) {
	var out bytes.Buffer
	_ = run([]string{"-help"}, &out, &out)
	if !strings.Contains(out.String(), "-trigger") || !strings.Contains(out.String(), "-rate") {
		t.Fatal("help is missing capture options")
	}
}

func TestPreset(t *testing.T) {
	for _, tc := range []struct {
		set      map[string]bool
		channels uint16
		rate     float64
		samples  int
		wait     time.Duration
	}{
		{nil, 0x000f, 1e6, 60_000_000, 150 * time.Second},
		{map[string]bool{"channels": true}, 0xffff, 1e6, 16_777_216, 63554432 * time.Microsecond},
		{map[string]bool{"rate": true, "wait": true}, 0x000f, 2e6, 67_108_864, time.Second},
	} {
		cfg := logic.Config{Channels: tc.channels, Rate: tc.rate, Samples: 12000}
		wait := time.Second
		if err := applyPreset("long", tc.set, "dslogic", &cfg, &wait); err != nil {
			t.Fatal(err)
		}
		if cfg.Channels != tc.channels || cfg.Rate != tc.rate ||
			cfg.Samples != tc.samples || wait != tc.wait {
			t.Fatalf("preset with %v: %+v wait %v", tc.set, cfg, wait)
		}
	}
}

func TestExistingOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.csv")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"-device", "bitscope", "-output", path}, &out, &out); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing output: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatal("existing output was changed")
	}
}
