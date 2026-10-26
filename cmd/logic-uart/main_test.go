package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/davecheney/logic"
)

// line builds a UART waveform on L2 at 1000 baud, 8 samples a bit.
type line struct{ s []byte }

func (l *line) level(v, bits int) {
	for range bits * 8 {
		l.s = append(l.s, byte(v<<2))
	}
}

func (l *line) frame(b byte, parity string, stop int) {
	l.level(0, 1)
	ones := 0
	for k := range 8 {
		v := int(b>>k) & 1
		ones += v
		l.level(v, 1)
	}
	switch parity {
	case "even":
		l.level(ones%2, 1)
	case "odd":
		l.level(1-ones%2, 1)
	}
	l.level(1, stop)
}

func (l *line) text(s string) {
	for i := range len(s) {
		l.frame(s[i], "none", 1)
	}
}

func (l *line) trace() *logic.Trace {
	return &logic.Trace{Rate: 8 * logic.KiloHertz, Channels: 1 << 2, TriggerAt: 8, Samples: l.s}
}

func TestFractionalRate(t *testing.T) {
	var l line
	l.level(1, 1)
	l.text("a")
	l.level(1, 1)
	l.text("b")
	l.level(1, 2)
	tr := l.trace()
	tr.Rate, tr.TriggerAt = 8500*logic.MilliHertz, 0
	var out, stderr bytes.Buffer
	if err := run([]string{"-line", "2", "-baud", "1.0625", "-gap", "500ms"},
		capture(t, tr), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	const want = "+0.941176471 \"a\"\n+11.294117647 \"b\"\n"
	if got := out.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func cfg() config { return config{line: 2, baud: 1000, parity: "none", stop: 1} }

func TestDecode(t *testing.T) {
	var l line
	l.level(1, 1)
	l.text("hi\r\nok\n")
	l.level(1, 200)
	l.text("x")
	l.level(1, 2)
	got := decode(l.trace(), cfg())
	want := []string{
		`+0.000000000 "hi\r\n"`,
		`+0.040000000 "ok\n"`,
		`+0.270000000 "x"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestErrors(t *testing.T) {
	var l line
	l.level(1, 1)
	l.frame('a', "none", 1)
	// A frame with its stop bit low.
	l.level(0, 1)
	l.level(1, 1)
	l.level(0, 8)
	l.level(1, 2)
	l.frame('b', "none", 1)
	l.level(1, 2)
	got := decode(l.trace(), cfg())
	if len(got) != 1 || !strings.HasSuffix(got[0], ` "a\x01b" framing=1`) {
		t.Fatalf("got %q", got)
	}
}

func TestParity(t *testing.T) {
	var l line
	l.level(1, 1)
	l.frame('a', "even", 1)
	l.frame('b', "odd", 1)
	l.level(1, 2)
	c := cfg()
	c.parity = "even"
	got := decode(l.trace(), c)
	if len(got) != 1 || !strings.HasSuffix(got[0], ` "ab" parity=1`) {
		t.Fatalf("got %q", got)
	}
}

func TestStopBits(t *testing.T) {
	var l line
	l.level(1, 1)
	l.frame(0x55, "none", 2)
	l.frame(0xaa, "none", 2)
	l.level(1, 2)
	c := cfg()
	c.stop = 2
	c.hex = true
	got := decode(l.trace(), c)
	if len(got) != 1 || !strings.HasSuffix(got[0], " 55 aa") {
		t.Fatalf("got %q", got)
	}
}

func TestGlitch(t *testing.T) {
	var l line
	l.level(1, 1)
	l.s = append(l.s, 0, 4, 4, 4)
	l.level(1, 1)
	l.text("z")
	l.level(1, 2)
	got := decode(l.trace(), cfg())
	if len(got) != 1 || !strings.HasSuffix(got[0], ` "z"`) {
		t.Fatalf("got %q", got)
	}
}

func capture(t *testing.T, tr *logic.Trace) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(tr); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestRun(t *testing.T) {
	var l line
	l.level(1, 1)
	l.text("ok\n")
	var out, stderr bytes.Buffer
	if err := run([]string{"-line", "2", "-baud", "1000"}, capture(t, l.trace()), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "+0.000000000 \"ok\\n\"\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInvalidOptions(t *testing.T) {
	var l line
	l.level(1, 4)
	for _, args := range [][]string{
		{}, {"-line", "16"}, {"-line", "1"}, {"-line", "2"},
		{"-line", "2", "-baud", "1000", "-parity", "mark"},
		{"-line", "2", "-baud", "1000", "-stop", "3"},
		{"-line", "2", "-baud", "0"},
		{"-line", "2", "-baud", "1000", "-gap", "-1s"},
		{"-line", "2", "-baud", "1000", "unexpected"},
	} {
		var out, stderr bytes.Buffer
		if err := run(args, capture(t, l.trace()), &out, &stderr); err == nil || out.Len() != 0 {
			t.Fatalf("args %v: invalid options accepted or wrote output", args)
		}
	}
}
