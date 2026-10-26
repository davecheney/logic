package logicpirate

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
)

func config() capture.Config {
	return capture.Config{
		Channels: 0x81, Source: 7, Rate: logic.MegaHertz, Samples: 4096,
		AnyEdge: true,
	}
}

func TestMetadata(t *testing.T) {
	for _, name := range []string{"Logic Pirate (40 MHz)", "Logic Pirate (60 MHz)"} {
		b := []byte("\x01" + name + "\x00\x022013-06-25\x00\x00")
		m, err := parseMetadata(b)
		if err != nil || m.name != name || m.firmware != "2013-06-25" ||
			m.clock != 40 && m.clock != 60 {
			t.Fatalf("metadata: %+v, %v", m, err)
		}
	}
	for _, b := range [][]byte{
		nil, {0}, []byte("\x01Logic Pirate (40 MHz)"),
		[]byte("\x01Open Logic Sniffer\x00\x022013-06-25\x00\x00"),
		[]byte("\x01Logic Pirate (40 MHz)\x00\x02\x00\x00"),
		[]byte("\x01Logic Pirate (40 MHz)\x00\x022013-06-25\x00"),
		[]byte("\x01Logic Pirate (40 MHz)\x00\x022013-06-25\x00\x00extra"),
	} {
		if _, err := parseMetadata(b); err == nil {
			t.Fatalf("accepted metadata %q", b)
		}
	}
}

func TestRateDividers(t *testing.T) {
	for _, clock := range []int{40, 60} {
		for _, setting := range ratesFor(clock) {
			c := config()
			c.Rate = setting.rate
			p, err := plan(c, ratesFor(clock))
			if err != nil {
				t.Fatal(err)
			}
			if p.rate != c.Rate || binary.LittleEndian.Uint32(p.commands(7, true)[1:]) != setting.divider {
				t.Fatalf("%d MHz firmware at %s: %+v", clock, c.Rate, p)
			}
		}
	}
	for _, tc := range []struct {
		clock int
		rate  logic.Frequency
	}{
		{40, 60 * logic.MegaHertz}, {40, 30 * logic.MegaHertz}, {40, 15 * logic.MegaHertz},
		{60, 40 * logic.MegaHertz}, {60, 20 * logic.MegaHertz},
		{40, 4 * logic.MegaHertz}, {60, 4 * logic.MegaHertz},
	} {
		c := config()
		c.Rate = tc.rate
		if _, err := plan(c, ratesFor(tc.clock)); err == nil {
			t.Fatalf("%d MHz firmware accepted %s", tc.clock, tc.rate)
		}
	}
}

func TestCommands(t *testing.T) {
	c := config()
	p, err := plan(c, ratesFor(40))
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x80, 99, 0, 0, 0,
		0x81, 0xfe, 0x0f, 0xff, 0x0f,
		0xc0, 0x80, 0, 0, 0,
		0x01,
	}
	if got := p.commands(7, true); !bytes.Equal(got, want) {
		t.Fatalf("commands: % x, want % x", got, want)
	}
	want[11] = 0
	if got := p.commands(7, false); !bytes.Equal(got, want) {
		t.Fatalf("free-running commands: % x, want % x", got, want)
	}
}

func TestInvalidPlan(t *testing.T) {
	for name, edit := range map[string]func(*capture.Config){
		"no inputs": func(c *capture.Config) { c.Channels = 0 },
		"input 8":   func(c *capture.Config) { c.Channels = 0x100 },
		"trigger":   func(c *capture.Config) { c.Source = 8 },
		"disabled":  func(c *capture.Config) { c.Source = 1 },
		"rising":    func(c *capture.Config) { c.AnyEdge = false },
		"falling":   func(c *capture.Config) { c.Falling = true },
		"too few":   func(c *capture.Config) { c.Samples = minSamples - 1 },
		"too many":  func(c *capture.Config) { c.Samples = maxSamples + 1 },
		"full RAM":  func(c *capture.Config) { c.Samples = 262144 },
		"zero rate": func(c *capture.Config) { c.Rate = 0 },
		"pre low":   func(c *capture.Config) { c.PreTrigger = -0.1 },
		"pre":       func(c *capture.Config) { c.PreTrigger = 0.05 },
		"pre NaN":   func(c *capture.Config) { c.PreTrigger = math.NaN() },
		"auto":      func(c *capture.Config) { c.Auto = -time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			c := config()
			edit(&c)
			if _, err := plan(c, ratesFor(40)); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}

func TestSampleCountsAndTrace(t *testing.T) {
	for _, n := range []int{4096, 4097, 12000, maxSamples - 1, maxSamples} {
		{
			c := config()
			c.Samples = n
			p, err := plan(c, ratesFor(40))
			if err != nil {
				t.Fatal(err)
			}
			size := binary.LittleEndian.Uint32(p.commands(c.Source, true)[6:10])
			readCount := (int(size&0xffff) + 2) * 4
			delay := (int(size>>16) + 1) * 4
			if readCount < n || readCount%transferBlock != 0 || readCount-n >= transferBlock ||
				readCount > maxSamples || delay != readCount {
				t.Fatalf("samples %d: read %d, delay %d", n, readCount, delay)
			}
			raw := make([]byte, readCount)
			for i := range raw {
				raw[i] = byte(i)
			}
			for _, triggered := range []bool{false, true} {
				tr, err := p.trace(c, raw, triggered)
				if err != nil {
					t.Fatal(err)
				}
				if tr.Len() != n || !tr.AnyEdge || tr.Falling || tr.Width != 1 ||
					tr.Rate != c.Rate || tr.Triggered != triggered || tr.TriggerAt < 0 || tr.TriggerAt >= n {
					t.Fatalf("trace: %+v", tr)
				}
				if tr.TriggerAt != 0 {
					t.Fatalf("trigger sample: %d", tr.TriggerAt)
				}
				for i, v := range tr.Samples {
					if v != byte(i)&0x81 {
						t.Fatalf("sample %d: got %#x", i, v)
					}
				}
			}
			if _, err := p.trace(c, raw[:len(raw)-1], true); err == nil {
				t.Fatal("accepted short transfer")
			}
		}
	}
}
