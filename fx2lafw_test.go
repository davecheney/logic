package logic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"testing"
	"testing/iotest"
	"time"
)

func fx2Config() Config {
	return Config{Channels: 0x81, Rate: MegaHertz, Samples: 6, Source: 7, PreTrigger: 0.5}
}

func TestFX2Validate(t *testing.T) {
	c := fx2Config()
	for _, rate := range fx2Rates {
		c.Rate = rate
		if err := Validate("fx2lafw", c); err != nil {
			t.Fatalf("rate %v: %v", rate, err)
		}
	}
	for name, change := range map[string]func(*Config){
		"no channels": func(c *Config) { c.Channels = 0 },
		"channel 8":   func(c *Config) { c.Channels = 0x100 },
		"source":      func(c *Config) { c.Source = 8 },
		"negative":    func(c *Config) { c.Source = -1 },
		"disabled":    func(c *Config) { c.Source = 1 },
		"rate":        func(c *Config) { c.Rate = 5 * MegaHertz },
		"too fast":    func(c *Config) { c.Rate = 48 * MegaHertz },
		"rate zero":   func(c *Config) { c.Rate = 0 },
		"rate mHz":    func(c *Config) { c.Rate = MegaHertz + 1 },
		"samples":     func(c *Config) { c.Samples = 1 },
		"pre low":     func(c *Config) { c.PreTrigger = -0.1 },
		"pre high":    func(c *Config) { c.PreTrigger = 1 },
		"pre NaN":     func(c *Config) { c.PreTrigger = math.NaN() },
		"auto":        func(c *Config) { c.Auto = -time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			c := fx2Config()
			change(&c)
			if err := Validate("fx2lafw", c); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if !slices.Contains(Devices(), "fx2lafw") {
		t.Fatal("driver is not registered")
	}
}

func TestFX2StartCommand(t *testing.T) {
	for rate, want := range map[Frequency][3]byte{
		24 * MegaHertz: {0x40, 0, 1},
		16 * MegaHertz: {0x40, 0, 2},
		3 * MegaHertz:  {0x40, 0, 15},
		1 * MegaHertz:  {0x40, 0, 47},
		25 * KiloHertz: {0, 0x04, 0xaf},
		20 * KiloHertz: {0, 0x05, 0xdb},
	} {
		got, err := fx2StartCommand(rate)
		if err != nil || got != want {
			t.Errorf("rate %v: %x %v, want %x", rate, got, err, want)
		}
	}
	for _, rate := range fx2Rates {
		if _, err := fx2StartCommand(rate); err != nil {
			t.Errorf("rate %v: %v", rate, err)
		}
	}
	for _, rate := range []Frequency{48 * MegaHertz, 7 * MegaHertz, 1 * KiloHertz, 500 * MilliHertz, 0, MegaHertz + 1, 1 << 63} {
		if _, err := fx2StartCommand(rate); err == nil {
			t.Errorf("rate %v accepted", rate)
		}
	}
}

func TestFX2Read(t *testing.T) {
	for _, tc := range []struct {
		name      string
		data      []byte
		want      []byte
		pre       float64
		falling   bool
		auto      time.Duration
		triggered bool
	}{
		{"rising", []byte{0, 1, 0, 1, 0xff, 0x80, 0x81},
			[]byte{1, 0, 1, 0x81, 0x80, 0x81}, 0.5, false, 0, true},
		{"falling", []byte{0x80, 0x81, 0x80, 0x81, 0x7f, 0, 1},
			[]byte{0x81, 0x80, 0x81, 1, 0, 1}, 0.5, true, 0, true},
		{"no pre", []byte{0, 0x80, 0x81, 0x80, 0x81, 0x80, 0x81},
			[]byte{0x80, 0x81, 0x80, 0x81, 0x80, 0x81}, 0, false, 0, true},
		{"ring wrap", []byte{0, 1, 0, 1, 0, 1, 0, 1, 0, 0x80, 0x81, 0x80},
			[]byte{0, 1, 0, 0x80, 0x81, 0x80}, 0.5, false, 0, true},
		{"auto", []byte{0, 1, 0, 1, 0, 1, 0, 1},
			[]byte{0, 1, 0, 1, 0, 1}, 0.5, false, 6 * time.Microsecond, false},
		{"edge before auto", []byte{0, 0, 0, 0x80, 0x80, 0x80},
			[]byte{0, 0, 0, 0x80, 0x80, 0x80}, 0.5, false, 6 * time.Microsecond, true},
		{"early edge ignored", []byte{0, 0x80, 0, 0, 0x80, 0x80, 0x80},
			[]byte{0x80, 0, 0, 0x80, 0x80, 0x80}, 0.5, false, 0, true},
		{"all but one pre", []byte{0, 1, 0, 1, 0, 0x80},
			[]byte{0, 1, 0, 1, 0, 0x80}, 0.9, false, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fx2Config()
			c.PreTrigger, c.Falling, c.Auto = tc.pre, tc.falling, tc.auto
			for _, r := range []io.Reader{bytes.NewReader(tc.data), iotest.OneByteReader(bytes.NewReader(tc.data))} {
				tr, err := fx2Read(context.Background(), r, c)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(tr.Samples, tc.want) || tr.TriggerAt != int(tc.pre*6) ||
					tr.Triggered != tc.triggered || tr.Rate != c.Rate || tr.Channels != c.Channels ||
					tr.Source != c.Source || tr.Falling != c.Falling || tr.Width != 1 {
					t.Fatalf("trace: %+v, samples %x; want %x", tr, tr.Samples, tc.want)
				}
			}
		})
	}
}

func TestFX2ReadErrors(t *testing.T) {
	c := fx2Config()
	for _, data := range [][]byte{nil, {0, 0, 0}, {0, 0, 0, 0x80}} {
		if _, err := fx2Read(context.Background(), bytes.NewReader(data), c); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("short capture: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fx2Read(ctx, bytes.NewReader(nil), c); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := fx2Read(context.Background(), iotest.ErrReader(io.ErrClosedPipe), c); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("read failure: %v", err)
	}
}
