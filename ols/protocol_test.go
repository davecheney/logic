package ols

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
)

// olsDeviceMetadata is the metadata reply of an Open Logic Sniffer v1.01
// with Demon Core 3.07.
const olsDeviceMetadata = "014f70656e204c6f67696320536e69666665722076312e30310002332e3037002100006000230bebc2004020410200"

func TestParseOLSMetadata(t *testing.T) {
	b, err := hex.DecodeString(olsDeviceMetadata)
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseOLSMetadata(b)
	if err != nil {
		t.Fatal(err)
	}
	want := olsMetadata{
		Name: "Open Logic Sniffer v1.01", FPGA: "3.07", Probes: 32,
		Memory: 24576, MaxRate: 200 * logic.MegaHertz, Protocol: 2,
	}
	if *m != want {
		t.Errorf("got %+v, want %+v", *m, want)
	}
}

func TestParseOLSMetadataErrors(t *testing.T) {
	for _, tc := range []struct {
		name, hex, err string
	}{
		{"empty", "", "no end marker"},
		{"no end", "4020", "no end marker"},
		{"open string", "01414243", "not terminated"},
		{"short uint32", "21000060", "is short"},
		{"short byte", "40", "is short"},
		{"bad type", "6000", "unknown type"},
	} {
		b, _ := hex.DecodeString(tc.hex)
		_, err := parseOLSMetadata(b)
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: got error %v, want %q", tc.name, err, tc.err)
		}
	}
}

func TestOLSPlan(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       capture.Config
		divider   uint32
		rate      logic.Frequency
		flags     uint32
		readCount int
		delay     int
		triggerAt int
		mask      [2]uint32
		value     [2]uint32
	}{{
		name:      "100 MHz, L0, rising, no pre-trigger",
		cfg:       capture.Config{Channels: 1, Rate: 100 * logic.MegaHertz, Samples: 1024},
		divider:   0,
		rate:      100 * logic.MegaHertz,
		flags:     0x38,
		readCount: 1024,
		delay:     1024,
		triggerAt: 0,
		mask:      [2]uint32{1, 1},
		value:     [2]uint32{0, 1},
	}, {
		name:      "1 MHz, L0-L15, falling L9, half pre-trigger",
		cfg:       capture.Config{Channels: 0xffff, Rate: logic.MegaHertz, Samples: 4096, Source: 9, Falling: true, PreTrigger: 0.5},
		divider:   99,
		rate:      logic.MegaHertz,
		flags:     0x30,
		readCount: 4096,
		delay:     2048,
		triggerAt: 2046,
		mask:      [2]uint32{1 << 9, 1 << 9},
		value:     [2]uint32{1 << 9, 0},
	}, {
		name:      "L8 only rounds rate down and samples up",
		cfg:       capture.Config{Channels: 0x0100, Rate: 3 * logic.MegaHertz, Samples: 10, Source: 8, PreTrigger: 1},
		divider:   33,
		rate:      logic.FrequencyFromHz(100e6 / 34),
		flags:     0x34,
		readCount: 12,
		delay:     4,
		triggerAt: 6,
		mask:      [2]uint32{1 << 8, 1 << 8},
		value:     [2]uint32{0, 1 << 8},
	}, {
		name:      "lowest rate",
		cfg:       capture.Config{Channels: 1, Rate: olsMinRate(), Samples: 4},
		divider:   16775708,
		rate:      logic.FrequencyFromHz(100e6 / 16775709),
		flags:     0x38,
		readCount: 4,
		delay:     4,
		triggerAt: 0,
		mask:      [2]uint32{1, 1},
		value:     [2]uint32{0, 1},
	}} {
		p, err := olsPlan(tc.cfg, 24576)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if p.divider != tc.divider || p.rate != tc.rate || p.flags != tc.flags ||
			p.readCount != tc.readCount || p.delay != tc.delay || p.triggerAt != tc.triggerAt ||
			p.mask != tc.mask || p.value != tc.value {
			t.Errorf("%s: got %+v", tc.name, *p)
		}
	}
}

func TestOLSPlanErrors(t *testing.T) {
	ok := capture.Config{Channels: 0x00ff, Rate: logic.MegaHertz, Samples: 1024}
	for _, tc := range []struct {
		name string
		edit func(*capture.Config)
		err  string
	}{
		{"no inputs", func(c *capture.Config) { c.Channels = 0 }, "at least one input"},
		{"zero rate", func(c *capture.Config) { c.Rate = 0 }, "sample rate"},
		{"too slow", func(c *capture.Config) { c.Rate = olsMinRate() - 1 }, "sample rate"},
		{"too fast", func(c *capture.Config) { c.Rate = 100*logic.MegaHertz + 1 }, "sample rate"},
		{"source off", func(c *capture.Config) { c.Source = 8 }, "trigger input"},
		{"source range", func(c *capture.Config) { c.Source = 16 }, "trigger input"},
		{"few samples", func(c *capture.Config) { c.Samples = 3 }, "between 4 and 24576"},
		{"one group full", func(c *capture.Config) { c.Samples = 24577 }, "between 4 and 24576"},
		{"two groups full", func(c *capture.Config) { c.Channels = 0x0101; c.Samples = 12289 }, "between 4 and 12288"},
		{"pre-trigger", func(c *capture.Config) { c.PreTrigger = 1.5 }, "pre-trigger"},
		{"auto", func(c *capture.Config) { c.Auto = -1 }, "auto"},
	} {
		c := ok
		tc.edit(&c)
		_, err := olsPlan(c, 24576)
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: got error %v, want %q", tc.name, err, tc.err)
		}
	}
	if _, err := olsPlan(ok, 0); err == nil {
		t.Error("no memory: got no error")
	}
	if _, err := olsPlan(ok, 24576); err != nil {
		t.Errorf("valid config: %v", err)
	}
}

func TestOLSCommands(t *testing.T) {
	p, err := olsPlan(capture.Config{Channels: 0xffff, Rate: logic.MegaHertz, Samples: 4096, Source: 9, Falling: true, PreTrigger: 0.5}, 24576)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 0, 0, 0, 0}
	for _, cmd := range [][]byte{
		{0xc0, 0x00, 0x02, 0x00, 0x00}, // stage 0 mask L9
		{0xc1, 0x00, 0x02, 0x00, 0x00}, // stage 0 waits for high
		{0xc2, 0x00, 0x00, 0x00, 0x00}, // stage 0, level 0
		{0xc4, 0x00, 0x02, 0x00, 0x00}, // stage 1 mask L9
		{0xc5, 0x00, 0x00, 0x00, 0x00}, // stage 1 waits for low
		{0xc6, 0x00, 0x00, 0x01, 0x08}, // stage 1, level 1, start
		{0x80, 99, 0x00, 0x00, 0x00},   // divider
		{0x81, 0xff, 0x03, 0xff, 0x01}, // read 1024-1, delay 512-1
		{0x82, 0x30, 0x00, 0x00, 0x00}, // groups 2 and 3 off
		{0x01},                         // arm
	} {
		want = append(want, cmd...)
	}
	if got := p.commands(); !bytes.Equal(got, want) {
		t.Errorf("got  % x\nwant % x", got, want)
	}
}

func TestOLSTrace(t *testing.T) {
	t.Run("one group", func(t *testing.T) {
		c := capture.Config{Channels: 0x0005, Rate: logic.MegaHertz, Samples: 6, Source: 2, PreTrigger: 0.5}
		p, err := olsPlan(c, 24576)
		if err != nil {
			t.Fatal(err)
		}
		raw := []byte{0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01, 0x00}
		tr, err := p.trace(c, raw, true)
		if err != nil {
			t.Fatal(err)
		}
		if want := []byte{0x00, 0x01, 0x00, 0x01, 0x04, 0x05}; !bytes.Equal(tr.Samples, want) {
			t.Errorf("samples: got % x, want % x", tr.Samples, want)
		}
		if tr.Width != 1 || tr.Len() != 6 || !tr.Triggered || tr.Rate != logic.MegaHertz || tr.TriggerAt != 2 {
			t.Errorf("got %+v", tr)
		}
	})
	t.Run("upper group only", func(t *testing.T) {
		c := capture.Config{Channels: 0xff00, Rate: logic.MegaHertz, Samples: 4, Source: 8}
		p, err := olsPlan(c, 24576)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := p.trace(c, []byte{0x04, 0x03, 0x02, 0x01}, false)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Width != 2 || tr.At(0) != 0x0100 || tr.At(3) != 0x0400 || tr.Triggered || tr.TriggerAt != 0 {
			t.Errorf("got %+v", tr)
		}
	})
	t.Run("two groups", func(t *testing.T) {
		c := capture.Config{Channels: 0xffff, Rate: logic.MegaHertz, Samples: 4}
		p, err := olsPlan(c, 24576)
		if err != nil {
			t.Fatal(err)
		}
		raw := []byte{0x31, 0x32, 0x21, 0x22, 0x11, 0x12, 0x01, 0x02}
		tr, err := p.trace(c, raw, true)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range []uint16{0x0201, 0x1211, 0x2221, 0x3231} {
			if got := tr.At(i); got != want {
				t.Errorf("sample %d: got %#04x, want %#04x", i, got, want)
			}
		}
		if _, err := p.trace(c, raw[1:], false); err == nil {
			t.Error("short transfer: got no error")
		}
	})
}
