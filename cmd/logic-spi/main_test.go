package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/davecheney/logic"
)

// Inputs: SCK L0, SDO L1, SDI L2, CS L3.
const (
	sck = 0
	sdo = 1
	sdi = 2
	cs  = 3
)

// bus builds an SPI waveform. Each half SCK period is h samples.
type bus struct {
	s                 []byte
	mode, h           int
	lsb               bool
	sck, sdo, sdi, cs int
}

func newBus(mode int) *bus {
	b := &bus{mode: mode, h: 4, cs: 1, sck: mode >> 1}
	b.hold(4)
	return b
}

func (b *bus) hold(n int) {
	for range n {
		b.s = append(b.s, byte(b.sck<<sck|b.sdo<<sdo|b.sdi<<sdi|b.cs<<cs))
	}
}

func (b *bus) select_(v int) { b.cs = v; b.hold(b.h) }

// bits sends n bits of w on SDO and r on SDI, and changes data on the
// edge that does not latch it.
func (b *bus) bits(w, r byte, n int) {
	cpol, cpha := b.mode>>1, b.mode&1
	for i := range n {
		k := 7 - i
		if b.lsb {
			k = i
		}
		set := func() { b.sdo, b.sdi = int(w>>k&1), int(r>>k&1) }
		if cpha == 0 {
			set()
			b.hold(b.h)
			b.sck = 1 - cpol
			b.hold(b.h)
			b.sck = cpol
		} else {
			b.sck = 1 - cpol
			set()
			b.hold(b.h)
			b.sck = cpol
			b.hold(b.h)
		}
	}
	b.hold(b.h)
}

func (b *bus) tx(w, r []byte) {
	for i := range w {
		b.bits(w[i], r[i], 8)
	}
}

func (b *bus) trace() *logic.Trace {
	return &logic.Trace{Rate: logic.MegaHertz, Inputs: logic.NewDevice(logic.ClassFX2).TraceInputs(0xf), Samples: b.s}
}

func capture(t *testing.T, tr *logic.Trace) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(tr); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func cfg(mode int) config { return config{sck: sck, sdo: sdo, sdi: sdi, cs: cs, mode: mode} }

// fields drops the time column of each line.
func fields(lines []string) []string {
	var out []string
	for _, l := range lines {
		_, rest, _ := strings.Cut(l, " ")
		out = append(out, rest)
	}
	return out
}

func check(t *testing.T, got []string, want ...string) {
	t.Helper()
	got = fields(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestModes(t *testing.T) {
	for mode := range 4 {
		for _, lsb := range []bool{false, true} {
			b := newBus(mode)
			b.lsb = lsb
			b.select_(0)
			b.tx([]byte{0x9f, 0x01}, []byte{0xa5, 0x80})
			b.select_(1)
			c := cfg(mode)
			c.lsb = lsb
			check(t, decode(b.trace(), c), "bits=16 sck=0.125MHz SDO 9f 01 SDI a5 80")
		}
	}
}

func TestWrongMode(t *testing.T) {
	b := newBus(1)
	b.select_(0)
	b.tx([]byte{0x55}, []byte{0xaa})
	b.select_(1)
	got := decode(b.trace(), cfg(0))
	if len(got) != 1 || !strings.Contains(got[0], " unstable=") {
		t.Fatalf("got %q, want one line with unstable=", got)
	}
}

func TestIdle(t *testing.T) {
	b := newBus(2)
	b.select_(0)
	b.tx([]byte{0x00}, []byte{0x00})
	b.select_(1)
	got := decode(b.trace(), cfg(1))
	if len(got) != 1 || !strings.HasSuffix(got[0], " idle=1") {
		t.Fatalf("got %q, want one line ending idle=1", got)
	}
}

func TestGapAndPartial(t *testing.T) {
	b := newBus(0)
	b.select_(0)
	b.bits(0x12, 0x34, 8)
	b.hold(40)
	b.bits(0x56, 0x78, 8)
	b.select_(1)
	b.select_(0)
	b.bits(0xe0, 0xe0, 3)
	b.select_(1)
	check(t, decode(b.trace(), cfg(0)),
		"bits=16 sck=0.125MHz SDO 12 56 SDI 34 78 gap=52µs",
		"bits=3 sck=0.125MHz SDO ? SDI ?")
}

func TestNoCS(t *testing.T) {
	b := newBus(0)
	b.bits(0x12, 0, 8)
	b.hold(100)
	b.bits(0x34, 0, 8)
	c := config{sck: sck, sdo: sdo, sdi: -1, cs: -1}
	check(t, decode(b.trace(), c), "bits=8 sck=0.125MHz SDO 12", "bits=8 sck=0.125MHz SDO 34")
	c.gap = time.Millisecond
	check(t, decode(b.trace(), c), "bits=16 sck=0.125MHz SDO 12 34 gap=112µs")
}

func TestRun(t *testing.T) {
	b := newBus(3)
	b.select_(0)
	b.tx([]byte{0x3c}, []byte{0xc3})
	b.select_(1)
	var out, stderr bytes.Buffer
	args := []string{"-sck", "0", "-sdo", "1", "-sdi", "2", "-cs", "3", "-mode", "3"}
	if err := run(args, capture(t, b.trace()), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if want := "+0.000004000 bits=8 sck=0.125MHz SDO 3c SDI c3\n"; out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestInvalidOptions(t *testing.T) {
	b := newBus(0)
	b.hold(4)
	for _, args := range [][]string{
		{}, {"-sck", "0"}, {"-sck", "0", "-sdo", "0"}, {"-sck", "0", "-sdo", "1", "-mode", "4"},
		{"-sck", "0", "-sdo", "16"}, {"-sck", "0", "-sdo", "1", "-cs", "3", "-gap", "1ms"},
		{"-sck", "0", "-sdo", "1", "-gap", "-1ms"}, {"-sck", "0", "-sdo", "1", "unexpected"},
	} {
		var out, stderr bytes.Buffer
		if err := run(args, capture(t, b.trace()), &out, &stderr); err == nil || out.Len() != 0 {
			t.Fatalf("args %v: invalid options accepted or wrote output", args)
		}
	}
}
