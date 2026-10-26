package main

import (
	"bytes"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davecheney/logic"
)

type testCapabilities []logic.Resource

func (c testCapabilities) Resources() iter.Seq[logic.Resource] {
	return slices.Values(c)
}

func TestInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-device", "nosuch"},
		{"-format", "xml"}, {"-channels", "256"}, {"-channels", "0"},
		{"-rate", "NaN"}, {"-rate", "16000000"}, {"-rate", "16MHz"}, {"-rate", "4mhz"}, {"-rate", "-1"}, {"-samples", "16000"},
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

func TestCaptureRequestRejectsNonDigitalResource(t *testing.T) {
	caps, err := logic.CapabilitiesFor("bitscope")
	if err != nil {
		t.Fatal(err)
	}
	resources := testCapabilities(slices.Collect(caps.Resources()))
	resources[0].Kind = logic.KindAnalogIn
	resources[0].Name = "A0"
	if _, err := captureRequest(resources, captureOptions{ChannelMask: 1, Source: 0}); err == nil || !strings.Contains(err.Error(), "not a digital input") {
		t.Fatalf("non-digital input: %v", err)
	}
}

func TestCaptureRequestUsesResourceRoles(t *testing.T) {
	caps, err := logic.CapabilitiesFor("bitscope")
	if err != nil {
		t.Fatal(err)
	}
	var input logic.Resource
	for r := range caps.Resources() {
		if r.ID.Kind() == logic.KindAnalogIn {
			input = r
			break
		}
	}
	if input.ID.Device() == nil {
		t.Fatal("no analog resource")
	}
	input.Kind |= logic.KindDigitalIn
	req, err := captureRequest(testCapabilities{input}, captureOptions{ChannelMask: 1, Source: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Inputs) != 1 || req.Inputs[0] != input.ID || req.Trigger != input.ID {
		t.Fatalf("capture request: %+v", req)
	}
}

func TestPreset(t *testing.T) {
	for _, tc := range []struct {
		set      map[string]bool
		channels uint
		rate     logic.Frequency
		samples  int
		wait     time.Duration
	}{
		{nil, 0x000f, logic.MegaHertz, 60_000_000, 150 * time.Second},
		{map[string]bool{"channels": true}, 0xffff, logic.MegaHertz, 16_777_216, 63554432 * time.Microsecond},
		{map[string]bool{"rate": true, "wait": true}, 0x000f, 2 * logic.MegaHertz, 67_108_864, time.Second},
	} {
		cfg := captureOptions{ChannelMask: tc.channels, Rate: tc.rate, Samples: 12000}
		wait := time.Second
		if err := applyPreset("long", tc.set, "dslogic", &cfg, &wait); err != nil {
			t.Fatal(err)
		}
		if cfg.ChannelMask != tc.channels || cfg.Rate != tc.rate ||
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
