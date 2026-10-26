package fx2lafw

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"math"
	"slices"
	"testing"
	"testing/iotest"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
	"github.com/davecheney/logic/internal/capturetest"
)

func fx2Config() capture.Config {
	return capture.Config{Channels: 0x81, Rate: logic.MegaHertz, Samples: 6, Source: 7, PreTrigger: 0.5}
}

func TestFX2Validate(t *testing.T) {
	dev := &FX2{device: logic.NewDevice(logic.ClassFX2)}
	c := fx2Config()
	for _, rate := range fx2Rates {
		c.Rate = rate
		if err := dev.ValidateCapture(capturetest.Request(dev, c)); err != nil {
			t.Fatalf("rate %v: %v", rate, err)
		}
	}
	for name, change := range map[string]func(*capture.Config){
		"no channels": func(c *capture.Config) { c.Channels = 0 },
		"channel 8":   func(c *capture.Config) { c.Channels = 0x100 },
		"source":      func(c *capture.Config) { c.Source = 8 },
		"negative":    func(c *capture.Config) { c.Source = -1 },
		"disabled":    func(c *capture.Config) { c.Source = 1 },
		"rate":        func(c *capture.Config) { c.Rate = 5 * logic.MegaHertz },
		"too fast":    func(c *capture.Config) { c.Rate = 48 * logic.MegaHertz },
		"rate zero":   func(c *capture.Config) { c.Rate = 0 },
		"rate mHz":    func(c *capture.Config) { c.Rate = logic.MegaHertz + 1 },
		"samples":     func(c *capture.Config) { c.Samples = 1 },
		"pre low":     func(c *capture.Config) { c.PreTrigger = -0.1 },
		"pre high":    func(c *capture.Config) { c.PreTrigger = 1 },
		"pre NaN":     func(c *capture.Config) { c.PreTrigger = math.NaN() },
		"auto":        func(c *capture.Config) { c.Auto = -time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			c := fx2Config()
			change(&c)
			if err := dev.ValidateCapture(capturetest.Request(dev, c)); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestFX2StartCommand(t *testing.T) {
	for rate, want := range map[logic.Frequency][3]byte{
		24 * logic.MegaHertz: {0x40, 0, 1},
		16 * logic.MegaHertz: {0x40, 0, 2},
		3 * logic.MegaHertz:  {0x40, 0, 15},
		1 * logic.MegaHertz:  {0x40, 0, 47},
		25 * logic.KiloHertz: {0, 0x04, 0xaf},
		20 * logic.KiloHertz: {0, 0x05, 0xdb},
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
	for _, rate := range []logic.Frequency{48 * logic.MegaHertz, 7 * logic.MegaHertz, 1 * logic.KiloHertz, 500 * logic.MilliHertz, 0, logic.MegaHertz + 1, 1 << 63} {
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

func TestFX2Capabilities(t *testing.T) {
	dev := logic.NewDevice(logic.ClassFX2)
	s := (&FX2{device: dev}).Capabilities()
	rs := slices.Collect(s.Resources())
	if len(rs) != 8 {
		t.Fatalf("%d resources, want 8", len(rs))
	}
	for _, r := range rs {
		if r.ID.Device() != dev {
			t.Fatalf("%s: ID %+v, want device %+v", r.Name, r.ID, dev)
		}
	}
	r := rs[0]
	if r.MinHz != 20*logic.KiloHertz || r.MaxHz != 24*logic.MegaHertz {
		t.Fatalf("range %v to %v", r.MinHz, r.MaxHz)
	}
	// A short selection, fastest first, from the maximum to the minimum.
	if len(r.Rates) >= len(fx2Rates) || r.Rates[0] != r.MaxHz || r.Rates[len(r.Rates)-1] != r.MinHz ||
		!slices.IsSortedFunc(r.Rates, func(a, b logic.Frequency) int { return cmp.Compare(b, a) }) {
		t.Fatalf("rates %v", r.Rates)
	}
	for _, rate := range r.Rates {
		if !slices.Contains(fx2Rates, rate) {
			t.Errorf("rate %v is not a firmware rate", rate)
		}
		if err := (&FX2{}).validateLogic(capture.Config{Channels: 1, Rate: rate, Samples: 2}); err != nil {
			t.Errorf("rate %v: %v", rate, err)
		}
	}
}

func TestFX2RejectsOutput(t *testing.T) {
	fx2 := &FX2{device: logic.NewDevice(logic.ClassFX2)}
	caps := fx2.Capabilities()
	if _, err := fx2.ConfigureAWG(logic.OutputRequest{
		Resource: slices.Collect(caps.Resources())[0].ID,
		Kind:     logic.KindDigitalOut,
		Enable:   true,
	}); err == nil {
		t.Fatal("unsupported output request accepted")
	}
}
