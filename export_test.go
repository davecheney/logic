package logic

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestReadWrite(t *testing.T) {
	for _, tr := range []*Trace{
		{Rate: MegaHertz, Inputs: NewDevice(ClassFX2).TraceInputs(3), TriggerAt: 2, Triggered: true, Samples: []byte{0, 1, 3, 2}},
		{Rate: 100 * MegaHertz, Inputs: NewDevice(ClassDSLogic).TraceInputs(0x8001), Width: 2, Samples: []byte{1, 0x80, 0, 0}},
		{Rate: 2500 * MilliHertz, Inputs: NewDevice(ClassBitScope).TraceInputs(1), Samples: []byte{0, 1}},
		{Rate: MilliHertz, Inputs: NewDevice(ClassBitScope).TraceInputs(1), Samples: []byte{0, 1}},
		{Rate: ^Frequency(0), Inputs: NewDevice(ClassBitScope).TraceInputs(1), Samples: []byte{0, 1}},
		{Rate: MegaHertz, Inputs: NewDevice(ClassLogicPirate).TraceInputs(0x81), AnyEdge: true, Triggered: true, Samples: []byte{0, 0x80}},
	} {
		var buf bytes.Buffer
		if err := Write(&buf, "json", tr); err != nil {
			t.Fatal(err)
		}
		got, err := Read(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got.Rate != tr.Rate || got.Mask() != tr.Mask() || got.TriggerAt != tr.TriggerAt ||
			got.Triggered != tr.Triggered || got.AnyEdge != tr.AnyEdge || got.Width != tr.Width || !bytes.Equal(got.Samples, tr.Samples) {
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
		`{"sample_rate":"1Hz","sample_bytes":4,"samples":"AAAAAAAA"}`,
		`{"sample_rate":"1Hz","inputs":[null,null,null,null,null,null,null,null,{"device":1,"index":8}],"samples":"AA=="}`,
		`{"sample_rate":"1Hz","inputs":[{"device":1,"class":"saleae","index":0}],"samples":"AA=="}`,
		`{"sample_rate":"1Hz","inputs":[{"device":1,"index":0},{"device":1,"index":0}],"samples":"AA=="}`,
		`{"sample_rate":"1Hz","inputs":[{"device":1,"class":"fx2lafw","index":0},{"device":1,"class":"dslogic","index":1}],"samples":"AA=="}`,
		`{"sample_rate":"1MHz","any_edge":true,"falling":true,"samples":"AAE="}`,
	} {
		if _, err := Read(strings.NewReader(s)); err == nil {
			t.Errorf("%q: accepted", s)
		}
	}
}

func TestAnyEdgeExport(t *testing.T) {
	dev := NewDevice(ClassLogicPirate)
	tr := &Trace{
		Rate: MegaHertz, Inputs: dev.TraceInputs(0x81), Trigger: dev.ResourceID(KindDigitalIn, 7),
		AnyEdge: true, Triggered: true, TriggerAt: 1, Samples: []byte{0, 0x80},
	}
	for _, format := range []string{"json", "csv", "vcd"} {
		var buf bytes.Buffer
		if err := Write(&buf, format, tr); err != nil {
			t.Fatal(err)
		}
		marker := "any_edge=true"
		if format == "json" {
			marker = `"any_edge":true`
			got, err := Read(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if !got.AnyEdge || got.Falling || got.Trigger.Index() != 7 ||
				got.Trigger.Device().Class() != ClassLogicPirate {
				t.Fatalf("round trip lost trigger or class: %+v", got)
			}
		}
		if strings.Count(buf.String(), marker) != 1 {
			t.Fatalf("%s: expected one any-edge marker: %s", format, &buf)
		}
	}
}

func TestFractionalRateExport(t *testing.T) {
	tr := &Trace{Rate: 2500 * MilliHertz, Inputs: NewDevice(ClassBitScope).TraceInputs(1), Samples: []byte{0, 1}}
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

func TestTraceInputs(t *testing.T) {
	d := NewDevice(ClassDSLogic)
	got := d.TraceInputs(0b101)
	want := []ResourceID{d.ResourceID(KindDigitalIn, 0), {}, d.ResourceID(KindDigitalIn, 2)}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if len(d.TraceInputs(0)) != 0 {
		t.Fatal("empty mask has inputs")
	}
}

func TestTraceWidth(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  uint64
	}{
		{0, 0x01}, {1, 0x01}, {2, 0x0201}, {4, 0x04030201}, {8, 0x0807060504030201},
	} {
		tr := Trace{Width: tc.width, Samples: []byte{1, 2, 3, 4, 5, 6, 7, 8, 0, 0, 0, 0, 0, 0, 0, 0}}
		if got := tr.At(0); got != tc.want {
			t.Errorf("width %d: At(0) = %#x, want %#x", tc.width, got, tc.want)
		}
		if got, want := tr.Len(), 16/max(tc.width, 1); got != want {
			t.Errorf("width %d: Len() = %d, want %d", tc.width, got, want)
		}
	}
}

// TestTraceDevices checks that a trace keeps the device of each input
// when its inputs come from more than one device.
func TestTraceDevices(t *testing.T) {
	dsl, bs := NewDevice(ClassDSLogic), NewDevice(ClassBitScope)
	inputs := append(dsl.TraceInputs(0x8001), bs.ResourceID(KindDigitalIn, 0), ResourceID{}, bs.ResourceID(KindDigitalIn, 2))
	tr := &Trace{
		Rate: MegaHertz, Inputs: inputs, Trigger: bs.ResourceID(KindDigitalIn, 2),
		Width: 4, Samples: []byte{0x01, 0x00, 0x01, 0x00, 0x00, 0x80, 0x04, 0x00},
	}
	if got := tr.Mask(); got != 0x58001 {
		t.Fatalf("Mask() = %#x", got)
	}
	if bit, ok := tr.Bit(tr.Trigger); !ok || bit != 18 {
		t.Fatalf("Bit(trigger) = %d, %t", bit, ok)
	}

	var buf bytes.Buffer
	if err := Write(&buf, "json", tr); err != nil {
		t.Fatal(err)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Inputs) != len(tr.Inputs) || got.Mask() != tr.Mask() || got.Width != 4 || got.At(1) != tr.At(1) {
		t.Fatalf("read %+v", got)
	}
	gotDSL, gotBS := got.Inputs[0].Device(), got.Inputs[16].Device()
	if gotDSL == gotBS || gotDSL.Class() != ClassDSLogic || gotBS.Class() != ClassBitScope ||
		got.Inputs[15].Device() != gotDSL || got.Inputs[18].Device() != gotBS {
		t.Fatalf("devices: %v", got.Inputs)
	}
	if got.Inputs[15].Index() != 15 || got.Inputs[18].Index() != 2 || got.Trigger != got.Inputs[18] {
		t.Fatalf("inputs %v, trigger %v", got.Inputs, got.Trigger)
	}

	buf.Reset()
	if err := Write(&buf, "csv", tr); err != nil {
		t.Fatal(err)
	}
	r := csv.NewReader(&buf)
	r.Comment = '#'
	rows, err := r.ReadAll()
	if err != nil || strings.Join(rows[0], ",") != "time_seconds,dslogic1.L0,dslogic1.L15,bitscope2.L0,bitscope2.L2" ||
		strings.Join(rows[1], ",") != "0,1,0,1,0" || strings.Join(rows[2], ",") != "1e-06,0,1,0,1" {
		t.Fatalf("CSV: %v, %v", rows, err)
	}
	buf.Reset()
	if err := Write(&buf, "vcd", tr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "$var wire 1 3 bitscope2.L2 $end") {
		t.Fatalf("VCD:\n%s", &buf)
	}
}
