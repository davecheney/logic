package logic

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestReadWrite(t *testing.T) {
	for _, tr := range []*Trace{
		{Rate: MegaHertz, Channels: 3, TriggerAt: 2, Triggered: true, Samples: []byte{0, 1, 3, 2}},
		{Rate: 100 * MegaHertz, Channels: 0x8001, Width: 2, Samples: []byte{1, 0x80, 0, 0}},
		{Rate: 2500 * MilliHertz, Channels: 1, Samples: []byte{0, 1}},
		{Rate: MilliHertz, Channels: 1, Samples: []byte{0, 1}},
		{Rate: ^Frequency(0), Channels: 1, Samples: []byte{0, 1}},
	} {
		var buf bytes.Buffer
		if err := Write(&buf, "json", tr); err != nil {
			t.Fatal(err)
		}
		got, err := Read(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got.Rate != tr.Rate || got.Channels != tr.Channels || got.TriggerAt != tr.TriggerAt ||
			got.Triggered != tr.Triggered || got.Width != tr.Width || !bytes.Equal(got.Samples, tr.Samples) {
			t.Fatalf("got %+v, want %+v", got, tr)
		}
	}
}

func TestTraceJSON(t *testing.T) {
	const input = `{"sample_rate":"4MHz","channels":1,"samples":"AAE="}`
	var tr Trace
	if err := json.Unmarshal([]byte(input), &tr); err != nil || tr.Rate != 4*MegaHertz {
		t.Fatalf("JSON decode: %+v, %v", tr, err)
	}
	for _, write := range []func(io.Writer) error{
		func(w io.Writer) error { return json.NewEncoder(w).Encode(&tr) },
		func(w io.Writer) error { return Write(w, "json", &tr) },
	} {
		var buf bytes.Buffer
		if err := write(&buf); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), `"sample_rate":"4MHz"`) ||
			strings.Contains(buf.String(), "sample_rate_hz") {
			t.Fatalf("unexpected rate encoding: %s", &buf)
		}
		got, err := Read(&buf)
		if err != nil || got.Rate != tr.Rate {
			t.Fatalf("read: %+v, %v", got, err)
		}
	}
}

func TestReadInvalid(t *testing.T) {
	for _, s := range []string{
		``, `{`, `{}`,
		`{"sample_rate_hz":4000000,"samples":"AA=="}`,
		`{"sample_rate":"0Hz","samples":"AA=="}`,
		`{"sample_rate":null,"samples":"AA=="}`,
		`{"sample_rate":4000000,"samples":"AA=="}`,
		`{"sample_rate":"-1Hz","samples":"AA=="}`,
		`{"sample_rate":"NaN","samples":"AA=="}`,
		`{"sample_rate":"Inf","samples":"AA=="}`,
		`{"sample_rate":"0.0001Hz","samples":"AA=="}`,
		`{"sample_rate":"18446744073709551.616Hz","samples":"AA=="}`,
		`{"sample_rate":"1Hz","samples":""}`,
		`{"sample_rate":"1Hz","sample_bytes":3,"samples":"AA=="}`,
		`{"sample_rate":"1Hz","sample_bytes":2,"samples":"AAAA"}`,
	} {
		if _, err := Read(strings.NewReader(s)); err == nil {
			t.Errorf("%q: accepted", s)
		}
	}
}

func TestFractionalRateExport(t *testing.T) {
	tr := &Trace{Rate: 2500 * MilliHertz, Channels: 1, Samples: []byte{0, 1}}
	var buf bytes.Buffer
	if err := Write(&buf, "csv", tr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "# sample_rate_hz=2.5\n") {
		t.Fatalf("CSV rate is not in Hertz: %s", &buf)
	}
	r := csv.NewReader(&buf)
	r.Comment = '#'
	rows, err := r.ReadAll()
	if err != nil || len(rows) != 3 || strings.Join(rows[2], ",") != "0.4,1" {
		t.Fatalf("CSV: %v, %v", rows, err)
	}
	buf.Reset()
	if err := Write(&buf, "vcd", tr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"sample_rate_hz=2.5 ",
		"#400000000000\n1!\n#800000000000\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("VCD missing %q: %s", want, &buf)
		}
	}
}
