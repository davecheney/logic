package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/davecheney/logic"
)

const (
	sda = 5
	scl = 6
)

// bus builds an I2C waveform with sda on L5 and scl on L6, four samples a step.
type bus struct{ s []byte }

func (b *bus) step(d, c int) {
	for range 4 {
		b.s = append(b.s, byte(d<<sda|c<<scl))
	}
}

func (b *bus) start() { b.step(1, 1); b.step(0, 1); b.step(0, 0) }

func (b *bus) restart() { b.step(1, 0); b.step(1, 1); b.step(0, 1); b.step(0, 0) }

func (b *bus) stop() { b.step(0, 0); b.step(0, 1); b.step(1, 1) }

func (b *bus) bit(v int) { b.step(v, 0); b.step(v, 1); b.step(v, 0) }

func (b *bus) byte(v byte, nack bool) {
	for i := 7; i >= 0; i-- {
		b.bit(int(v>>i) & 1)
	}
	if nack {
		b.bit(1)
	} else {
		b.bit(0)
	}
}

func (b *bus) trace() *logic.Trace {
	return &logic.Trace{Rate: logic.MegaHertz, Channels: 1<<sda | 1<<scl, Samples: b.s}
}

func capture(t *testing.T, tr *logic.Trace) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(tr); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestDecode(t *testing.T) {
	var b bus
	b.step(1, 1)
	// Write register 0 to 0x3c, then read two bytes.
	b.start()
	b.byte(0x3c<<1, false)
	b.byte(0x00, false)
	b.restart()
	b.byte(0x3c<<1|1, false)
	b.byte(0x42, false)
	b.byte(0x43, true)
	b.stop()
	// An address with no reply.
	b.start()
	b.byte(0x08<<1|1, true)
	b.stop()
	// A transaction cut short by the end of the capture.
	b.start()
	b.byte(0x50<<1, false)

	got := decode(b.trace(), sda, scl)
	want := []string{
		"+0.000008000 S 3c W A 00 A Sr 3c R A 42 A 43 N P",
		"S 08 R N P",
		"S 50 W A",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	if got[0] != want[0] {
		t.Errorf("line 0: got %q, want %q", got[0], want[0])
	}
	for i := 1; i < len(want); i++ {
		if _, rest, _ := strings.Cut(got[i], " "); rest != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCutShort(t *testing.T) {
	var b bus
	b.step(1, 1)
	b.start()
	b.bit(0)
	b.bit(1)
	b.bit(1)
	b.stop()
	got := decode(b.trace(), sda, scl)
	if len(got) != 1 || !strings.HasSuffix(got[0], " S ? P") {
		t.Fatalf("got %q, want one line ending S ? P", got)
	}
}

func TestRun(t *testing.T) {
	var b bus
	b.step(1, 1)
	b.start()
	b.byte(0x3c<<1, false)
	b.stop()
	for _, tc := range []struct {
		rate logic.Frequency
		want string
	}{
		{logic.MegaHertz, "+0.000008000 S 3c W A P\n"},
		{2500 * logic.MilliHertz, "+3.200000000 S 3c W A P\n"},
	} {
		tr := b.trace()
		tr.Rate = tc.rate
		var out, stderr bytes.Buffer
		if err := run([]string{"-sda", "5", "-scl", "6"}, capture(t, tr), &out, &stderr); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != tc.want {
			t.Fatalf("rate %v: got %q, want %q", tc.rate, got, tc.want)
		}
	}
}

func TestInvalidOptions(t *testing.T) {
	var b bus
	b.step(1, 1)
	for _, args := range [][]string{
		{}, {"-sda", "5"}, {"-sda", "5", "-scl", "5"}, {"-sda", "16", "-scl", "6"},
		{"-sda", "0", "-scl", "6"}, {"-sda", "5", "-scl", "6", "unexpected"},
	} {
		var out, stderr bytes.Buffer
		if err := run(args, capture(t, b.trace()), &out, &stderr); err == nil || out.Len() != 0 {
			t.Fatalf("args %v: invalid options accepted or wrote output", args)
		}
	}
}
