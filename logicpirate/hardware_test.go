package logicpirate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capturetest"
)

// Connect the stock TEST output (1 kHz, 75% high) to input 0.
func TestHardware(t *testing.T) {
	port := os.Getenv("LOGICPIRATE_TEST_PORT")
	if port == "" {
		t.Skip("set LOGICPIRATE_TEST_PORT and connect TEST to input 0")
	}
	d, err := Open(port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, tc := range []struct {
		name    string
		samples int
		rate    logic.Frequency
	}{
		{"minimum", minSamples, logic.MegaHertz},
		{"partial block", 12001, 2 * logic.MegaHertz},
		{"timing", 65536, logic.MegaHertz},
		{"maximum", maxSamples, logic.Frequency(d.meta.clock) * logic.MegaHertz},
		{"slowest", minSamples, 2 * logic.KiloHertz},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config()
			c.Channels, c.Source, c.PreTrigger = 1, 0, 0
			c.Rate, c.Samples = tc.rate, tc.samples
			if tc.name == "partial block" {
				c.Auto = time.Second
			}
			req := capturetest.Request(d, c)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			tr, err := d.CaptureLogic(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if tr.Len() != tc.samples || tr.Rate != tc.rate || !tr.Triggered ||
				!tr.AnyEdge || tr.Mask() != 1 || tr.Trigger != req.Trigger || tr.TriggerAt != 0 {
				t.Fatalf("capture metadata: samples %d, rate %s, triggered %t, any-edge %t, mask %#x, trigger %d",
					tr.Len(), tr.Rate, tr.Triggered, tr.AnyEdge, tr.Mask(), tr.TriggerAt)
			}
			for i, v := range tr.Samples {
				if v&^1 != 0 {
					t.Fatalf("sample %d has disabled bits %#x", i, v)
				}
			}
			var encoded bytes.Buffer
			if err := logic.Write(&encoded, "json", tr); err != nil {
				t.Fatal(err)
			}
			got, err := logic.Read(&encoded)
			if err != nil || !got.AnyEdge || got.Trigger.Device().Class() != logic.ClassLogicPirate ||
				!bytes.Equal(got.Samples, tr.Samples) {
				t.Fatalf("JSON round trip: %v", err)
			}
			if tc.name == "timing" || tc.name == "maximum" {
				checkTestSignal(t, tr)
			}
		})
	}
	t.Run("cancel and reuse", func(t *testing.T) {
		c := config()
		c.Channels, c.Source, c.PreTrigger = 1, 0, 0
		c.Rate = 2 * logic.KiloHertz
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := d.CaptureLogic(ctx, capturetest.Request(d, c))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancel slow capture: %v", err)
		}
		c.Rate = logic.MegaHertz
		retry, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		if _, err := d.CaptureLogic(retry, capturetest.Request(d, c)); err != nil {
			t.Fatalf("capture after cancellation: %v", err)
		}
	})
}

func checkTestSignal(t *testing.T, tr *logic.Trace) {
	t.Helper()
	var rising, high, periods []int
	for i := 1; i < tr.Len(); i++ {
		before, after := tr.Samples[i-1]&1, tr.Samples[i]&1
		if before == 0 && after == 1 {
			if len(rising) > 0 {
				periods = append(periods, i-rising[len(rising)-1])
			}
			rising = append(rising, i)
		} else if before == 1 && after == 0 && len(rising) > 0 {
			high = append(high, i-rising[len(rising)-1])
		}
	}
	if len(periods) < 3 || len(high) < 3 {
		t.Fatalf("too few TEST edges: %d periods, %d high pulses", len(periods), len(high))
	}
	slices.Sort(periods)
	slices.Sort(high)
	period, pulse := periods[len(periods)/2], high[len(high)/2]
	want := int(tr.Rate / (1000 * logic.Hertz))
	tolerance := max(5, want/200)
	if period < want-tolerance || period > want+tolerance ||
		pulse < want*3/4-tolerance || pulse > want*3/4+tolerance {
		t.Fatalf("TEST at %s: median period %d samples, high %d; want %d and %d (+/- %d)",
			tr.Rate, period, pulse, want, want*3/4, tolerance)
	}
	t.Logf("TEST at %s: median period %d samples, high %d samples", tr.Rate, period, pulse)
}
