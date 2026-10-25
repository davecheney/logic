package logic

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadWrite(t *testing.T) {
	for _, tr := range []*Trace{
		{Rate: 1e6, Channels: 3, TriggerAt: 2, Triggered: true, Samples: []byte{0, 1, 3, 2}},
		{Rate: 1e8, Channels: 0x8001, Width: 2, Samples: []byte{1, 0x80, 0, 0}},
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

func TestReadInvalid(t *testing.T) {
	for _, s := range []string{
		``, `{`, `{}`,
		`{"sample_rate_hz":0,"samples":"AA=="}`,
		`{"sample_rate_hz":1,"samples":""}`,
		`{"sample_rate_hz":1,"sample_bytes":3,"samples":"AA=="}`,
		`{"sample_rate_hz":1,"sample_bytes":2,"samples":"AAAA"}`,
	} {
		if _, err := Read(strings.NewReader(s)); err == nil {
			t.Errorf("%q: accepted", s)
		}
	}
}
