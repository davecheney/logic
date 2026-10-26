package logic

import (
	"encoding/json"
	"testing"
)

func TestFrequencyString(t *testing.T) {
	for f, want := range map[Frequency]string{
		0:                         "0Hz",
		1:                         "1mHz",
		500 * MilliHertz:          "500mHz",
		Hertz:                     "1Hz",
		1500 * MilliHertz:         "1.5Hz",
		2442 * Hertz:              "2.442kHz",
		4 * MegaHertz:             "4MHz",
		400 * MegaHertz:           "400MHz",
		MegaHertz + 1:             "1.000000001MHz",
		2*GigaHertz + 5*MegaHertz: "2.005GHz",
	} {
		if got := f.String(); got != want {
			t.Errorf("%d: got %q, want %q", uint64(f), got, want)
		}
	}
}

func TestParseFrequency(t *testing.T) {
	for s, want := range map[string]Frequency{
		"0":              0,
		"4000000":        4 * MegaHertz,
		"4e6":            4 * MegaHertz,
		"4E6":            4 * MegaHertz,
		"4MHz":           4 * MegaHertz,
		"1.5kHz":         1500 * Hertz,
		"500mHz":         500 * MilliHertz,
		"0.5":            500 * MilliHertz,
		"24e6Hz":         24 * MegaHertz,
		"1GHz":           GigaHertz,
		"0.001":          MilliHertz,
		"400MHz":         400 * MegaHertz,
		"20000":          20 * KiloHertz,
		"1.000000001MHz": MegaHertz + 1,
	} {
		got, err := ParseFrequency(s)
		if err != nil || got != want {
			t.Errorf("%q: got %v %v, want %v", s, got, err, want)
		}
	}
	for _, s := range []string{
		"", "Hz", "MHz", "-1", "+1", "1/2", "NaN", "Inf", "0x10", "1 MHz",
		"1mhz", "1.2.3", "0.0001", "1e-4", "2.5e-3", "1e100", "e6", "1e", "20000000000000000",
	} {
		if f, err := ParseFrequency(s); err == nil {
			t.Errorf("%q accepted as %v", s, f)
		}
	}
}

func TestFrequencyRoundTrip(t *testing.T) {
	for _, f := range []Frequency{1, 999, Hertz, 2442 * Hertz, 24 * MegaHertz, MegaHertz + 1, 18 * GigaHertz} {
		got, err := ParseFrequency(f.String())
		if err != nil || got != f {
			t.Errorf("%v: got %v %v", f, got, err)
		}
	}
}

func TestFrequencyHz(t *testing.T) {
	if got := (1500 * MilliHertz).Hz(); got != 1.5 {
		t.Fatalf("Hz: got %g", got)
	}
}

func TestFrequencyJSON(t *testing.T) {
	for f, want := range map[Frequency]string{
		0:                 `"0Hz"`,
		MilliHertz:        `"1mHz"`,
		500 * MilliHertz:  `"500mHz"`,
		Hertz:             `"1Hz"`,
		2500 * MilliHertz: `"2.5Hz"`,
		4 * MegaHertz:     `"4MHz"`,
		MegaHertz + 1:     `"1.000000001MHz"`,
		^Frequency(0):     `"18446744.073709551615GHz"`,
	} {
		b, err := json.Marshal(f)
		if err != nil || string(b) != want {
			t.Fatalf("%v: JSON %s, %v; want %s", f, b, err, want)
		}
		var got Frequency
		if err := json.Unmarshal(b, &got); err != nil || got != f {
			t.Errorf("%s: got %v, %v; want %v", b, got, err, f)
		}
	}
}

func TestFrequencyUnmarshalJSON(t *testing.T) {
	for s, want := range map[string]Frequency{
		`"4e6"`:       4 * MegaHertz,
		`"1.5kHz"`:    1500 * Hertz,
		`"\u0034MHz"`: 4 * MegaHertz,
		`"1000mHz"`:   Hertz,
		`"0.001Hz"`:   MilliHertz,
	} {
		var got Frequency
		if err := json.Unmarshal([]byte(s), &got); err != nil || got != want {
			t.Errorf("%s: got %v, %v; want %v", s, got, err, want)
		}
	}
	for _, s := range []string{
		``, `"`, `""`, `null`, `true`, `false`, `4`, `4e6`, `[]`, `{}`,
		`"-1Hz"`, `"NaN"`, `"Inf"`, `"1mhz"`, `"0.0001Hz"`,
		`"18446744073709551.616Hz"`, `"1e100Hz"`,
	} {
		got := Hertz
		if err := json.Unmarshal([]byte(s), &got); err == nil {
			t.Errorf("%s: accepted as %v", s, got)
		}
		if got != Hertz {
			t.Errorf("%s: changed receiver on error to %v", s, got)
		}
	}
}

func TestFrequencyFromHz(t *testing.T) {
	for _, tc := range []struct {
		hz   float64
		want Frequency
	}{
		{0.0014, MilliHertz},
		{0.0015, 2 * MilliHertz},
		{0.0016, 2 * MilliHertz},
		{4e6, 4 * MegaHertz},
		{400e6, 400 * MegaHertz},
	} {
		if got := FrequencyFromHz(tc.hz); got != tc.want {
			t.Errorf("%g Hz: got %v, want %v", tc.hz, got, tc.want)
		}
	}
}

func TestFrequencyDividerRounding(t *testing.T) {
	for _, tc := range []struct {
		clock Frequency
		first uint64
		last  uint64
	}{
		{40 * MegaHertz, 5, 16384},
		{100 * MegaHertz, 1, 2000},
	} {
		for div := tc.first; div <= tc.last; div++ {
			got := FrequencyFromHz(tc.clock.Hz() / float64(div))
			want := Frequency((uint64(tc.clock) + div/2) / div)
			if got != want {
				t.Fatalf("%v / %d: got %v, want %v", tc.clock, div, got, want)
			}
		}
	}
}
