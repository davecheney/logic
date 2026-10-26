package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/davecheney/logic"
)

// trace has L0 and L1 at 1 MS/s with the trigger at sample 2.
func trace(t *testing.T) *bytes.Buffer {
	t.Helper()
	tr := &logic.Trace{Rate: logic.MegaHertz, Channels: 3, TriggerAt: 2, Triggered: true,
		Samples: []byte{0, 1, 1, 3, 3, 2, 0}}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(tr); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestFractionalRate(t *testing.T) {
	const input = `{"sample_rate":"2.5Hz","channels":1,"trigger_sample":1,"samples":"AAEA"}`
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "# sample_rate_hz=2.5 trigger_sample=1 triggered=false start=-0.400000000 L0=0\n" +
			"+0.000000000 L0 1\n+0.400000000 L0 0\n"},
		{[]string{"-from", "0s", "-to", "400ms"},
			"# sample_rate_hz=2.5 trigger_sample=1 triggered=false start=+0.000000000 L0=1\n" +
				"+0.400000000 L0 0\n"},
	} {
		var out, stderr bytes.Buffer
		if err := run(tc.args, bytes.NewBufferString(input), &out, &stderr); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != tc.want {
			t.Errorf("args %v: got %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestEdges(t *testing.T) {
	const header = "# sample_rate_hz=1e+06 trigger_sample=2 triggered=true "
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, header + "start=-0.000002000 L0=0 L1=0\n" +
			"-0.000001000 L0 1\n+0.000001000 L1 1\n+0.000003000 L0 0\n+0.000004000 L1 0\n"},
		{[]string{"-channels", "2"}, header + "start=-0.000002000 L1=0\n" +
			"+0.000001000 L1 1\n+0.000004000 L1 0\n"},
		{[]string{"-from", "0s", "-to", "3us"}, header + "start=+0.000000000 L0=1 L1=0\n" +
			"+0.000001000 L1 1\n+0.000003000 L0 0\n"},
		{[]string{"-limit", "1"}, header + "start=-0.000002000 L0=0 L1=0\n" +
			"-0.000001000 L0 1\n"},
	} {
		var out, stderr bytes.Buffer
		if err := run(tc.args, trace(t), &out, &stderr); err != nil {
			t.Fatalf("args %v: %v", tc.args, err)
		}
		if out.String() != tc.want {
			t.Errorf("args %v:\ngot\n%s\nwant\n%s", tc.args, out.String(), tc.want)
		}
	}
}

func TestInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{"-channels", "4"}, {"-channels", "65536"}, {"-limit", "-1"},
		{"-from", "1ms"}, {"-from", "3us", "-to", "1us"}, {"unexpected"},
	} {
		var out, stderr bytes.Buffer
		if err := run(args, trace(t), &out, &stderr); err == nil || out.Len() != 0 {
			t.Fatalf("args %v: invalid options accepted or wrote output", args)
		}
	}
}

func TestBadInput(t *testing.T) {
	var out, stderr bytes.Buffer
	if err := run(nil, bytes.NewBufferString("{}"), &out, &stderr); err == nil {
		t.Fatal("empty capture accepted")
	}
}
