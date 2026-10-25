package logic

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"
	"time"
)

func TestRegisters(t *testing.T) {
	var r registers
	r.u8(0x21, 0x12)
	r.u16(0x2e, 0x0428)
	if got, want := r.String(), "21@12s2e@28s2f@04s"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestChoose(t *testing.T) {
	tests := []struct {
		channels int
		span     float64
		samples  int
		mode     string
	}{
		{1, 10e-3, 1024, "Macro"},       // 100 kS/s, 16 bit
		{2, 10e-3, 1024, "MacroChop"},   // 200 kS/s total, 16 bit
		{1, 1e-3, 1024, "Analog"},       // 1 MS/s, 8 bit
		{2, 1e-3, 1024, "AnalogChop"},   // 2 MS/s total, 8 bit
		{1, 100e-6, 1024, "AnalogShot"}, // 10 MS/s
		{1, 1, 1024, "Macro"},           // slow
	}
	for _, tt := range tests {
		p, err := choose(tt.channels, tt.span, tt.samples)
		if err != nil {
			t.Fatalf("choose(%d, %v, %d): %v", tt.channels, tt.span, tt.samples, err)
		}
		if p.mode.name != tt.mode {
			t.Errorf("choose(%d, %v, %d): mode %s, want %s", tt.channels, tt.span, tt.samples, p.mode.name, tt.mode)
		}
		got := float64(p.samples) / p.rate()
		if math.Abs(got-tt.span)/tt.span > 0.05 {
			t.Errorf("choose(%d, %v, %d): span %v", tt.channels, tt.span, tt.samples, got)
		}
	}
}

// TestDevice runs a capture on real hardware. Set BITSCOPE_PORT to the
// serial port of the device to run it.
func TestDevice(t *testing.T) {
	port := os.Getenv("BITSCOPE_PORT")
	if port == "" {
		t.Skip("BITSCOPE_PORT not set")
	}
	s, err := OpenBitScope(port)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Logf("device %s", s.ID)
	for _, span := range []time.Duration{100 * time.Microsecond, time.Millisecond, 10 * time.Millisecond, 100 * time.Millisecond} {
		for _, ab := range [][2]bool{{true, false}, {false, true}, {true, true}} {
			start := time.Now()
			source := A
			if !ab[0] {
				source = B
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			tr, err := s.Capture(ctx, ScopeConfig{
				A: ab[0], B: ab[1], Duration: span, Low: -5.5, High: 8,
				Trigger: Trigger{Source: source, Level: 0, Position: 0.25, Timeout: 50 * time.Millisecond},
			})
			cancel()
			if err != nil {
				t.Fatalf("span %v A=%v B=%v: %v", span, ab[0], ab[1], err)
			}
			if tr.Rate <= 0 || (len(tr.A) > 0) != ab[0] || (len(tr.B) > 0) != ab[1] {
				t.Fatalf("invalid trace: rate=%v A=%d B=%d", tr.Rate, len(tr.A), len(tr.B))
			}
			for _, samples := range [][]float64{tr.A, tr.B} {
				for _, v := range samples {
					if math.IsNaN(v) || math.IsInf(v, 0) || v < -5.5 || v > 8 {
						t.Fatalf("invalid sample: %v", v)
					}
				}
			}
			lo, hi := math.Inf(1), math.Inf(-1)
			values := tr.A
			if !ab[0] {
				values = tr.B
			}
			for _, v := range values {
				lo, hi = min(lo, v), max(hi, v)
			}
			if relative := math.Abs(float64(tr.Duration-span)) / float64(span); relative > 0.05 {
				t.Fatalf("trace duration %v, want %v", tr.Duration, span)
			}
			t.Logf("span %v: %s %.0f S/s, %d samples A, %d B, triggered %v, %s %.2f..%.2f V, %v",
				span, tr.Mode, tr.Rate, len(tr.A), len(tr.B), tr.Triggered, source, lo, hi, time.Since(start).Round(time.Millisecond))
		}
	}
	c := ScopeConfig{
		A: true, Duration: time.Millisecond, Low: -5.5, High: 8,
		Trigger: Trigger{Level: 8, Position: 0.25},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Capture(ctx, c); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled capture: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, err = s.Capture(ctx, c)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("normal trigger cancellation: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c.Trigger.Timeout = 50 * time.Millisecond
	if _, err := s.Capture(ctx, c); err != nil {
		t.Fatalf("capture after cancellation: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	valid := ScopeConfig{
		A: true, Duration: time.Millisecond, Low: -1, High: 1,
		Trigger: Trigger{Level: 0, Position: 0.25},
	}
	for name, change := range map[string]func(*ScopeConfig){
		"no channels": func(c *ScopeConfig) { c.A = false },
		"zero time":   func(c *ScopeConfig) { c.Duration = 0 },
		"few samples": func(c *ScopeConfig) { c.Samples = 1 },
		"NaN":         func(c *ScopeConfig) { c.Low = math.NaN() },
		"range":       func(c *ScopeConfig) { c.High = c.Low },
		"source":      func(c *ScopeConfig) { c.Trigger.Source = B },
		"position":    func(c *ScopeConfig) { c.Trigger.Position = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid
			change(&c)
			s := &BitScope{}
			if _, err := s.Capture(context.Background(), c); err == nil {
				t.Fatal("invalid config was accepted")
			}
		})
	}
}
