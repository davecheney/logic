package bitscope

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
	"github.com/davecheney/logic/internal/capturetest"
)

func validLogicConfig() capture.Config {
	return capture.Config{Channels: 3, Rate: 4 * logic.MegaHertz, Samples: 12000, Source: 1, PreTrigger: 0.05}
}

func TestLogicPlan(t *testing.T) {
	c := validLogicConfig()
	ticks, trigger, timeout, err := bitscopeLogicPlan(c)
	if err != nil || ticks != 10 || trigger != 600 || timeout != 0 {
		t.Fatalf("plan: %d %d %d %v", ticks, trigger, timeout, err)
	}
	if post := float64(c.Samples-trigger) * float64(ticks) / clockRate; post < 0.001 {
		t.Fatalf("SPI capture has only %g seconds after trigger", post)
	}
	for _, rate := range []logic.Frequency{2442 * logic.Hertz, 1 * logic.MegaHertz, 3 * logic.MegaHertz, 4 * logic.MegaHertz, 5 * logic.MegaHertz, 8 * logic.MegaHertz} {
		c.Rate, c.PreTrigger = rate, 0
		ticks, _, _, err := bitscopeLogicPlan(c)
		if err != nil || clockRate/float64(ticks) > rate.Hz() || ticks < 5 || ticks > 16384 {
			t.Fatalf("rate %v: ticks=%d %v", rate, ticks, err)
		}
	}
	for name, change := range map[string]func(*capture.Config){
		"empty channels": func(c *capture.Config) { c.Channels = 0 },
		"analog bit":     func(c *capture.Config) { c.Channels = 0x80 },
		"source off":     func(c *capture.Config) { c.Source = 2 },
		"source low":     func(c *capture.Config) { c.Source = -1 },
		"source high":    func(c *capture.Config) { c.Source = 6 },
		"rate low":       func(c *capture.Config) { c.Rate = 2441 * logic.Hertz },
		"rate high":      func(c *capture.Config) { c.Rate = 8*logic.MegaHertz + 1 },
		"rate zero":      func(c *capture.Config) { c.Rate = 0 },
		"few samples":    func(c *capture.Config) { c.Samples = 31 },
		"buffer":         func(c *capture.Config) { c.Samples = 12289 },
		"pre negative":   func(c *capture.Config) { c.PreTrigger = -0.1 },
		"pre high":       func(c *capture.Config) { c.PreTrigger = 1.1 },
		"pre nan":        func(c *capture.Config) { c.PreTrigger = math.NaN() },
		"pre fast":       func(c *capture.Config) { c.Rate = 8 * logic.MegaHertz },
		"auto negative":  func(c *capture.Config) { c.Auto = -time.Second },
		"auto overflow":  func(c *capture.Config) { c.Auto = time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			c := validLogicConfig()
			change(&c)
			if (&BitScope{}).validateLogic(c) == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestLogicCapture(t *testing.T) {
	for _, chunk := range []int{1, 3, 4096} {
		for _, status := range []string{"00", "01"} {
			c := validLogicConfig()
			c.Samples = 32
			c.Falling = true
			raw := bytes.Repeat([]byte{0, 1, 2, 0xff}, 8)
			p := &testPort{chunk: chunk, reply: func(cmd string) string {
				switch cmd {
				case "7c@p":
					return cmd + "\r80\r"
				case "D":
					return cmd + "02\r1234\r" + status + "\r2345\r000010\r"
				case "A":
					return cmd + string(raw)
				default:
					return cmd
				}
			}}
			s := &BitScope{name: "BS000500", device: logic.NewDevice(logic.ClassBitScope), vm: vm{port: p, timeout: time.Second}}
			tr, err := s.captureLogic(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if tr.Rate != 4*logic.MegaHertz || tr.TriggerAt != 1 || tr.Triggered != (status == "00") ||
				tr.Mask() != 3 || tr.Trigger != s.device.ResourceID(logic.KindDigitalIn, 1) || !tr.Falling || len(tr.Samples) != 32 {
				t.Fatalf("trace: %+v", tr)
			}
			for i, sample := range tr.Samples {
				if sample != raw[i]&3 {
					t.Fatalf("sample %d: %d", i, sample)
				}
			}
			for _, want := range []string{
				"21@0es31@00s", "2e@0as2f@00s", "05@02s06@fds",
				"32@01s33@00s34@01s35@00s", "07@41s",
				"7b@00s7c@80s37@00s38@03s", "08@f0s09@2fs0a@00s",
				"1e@00s30@80s1c@20s1d@00s",
			} {
				if !strings.Contains(p.wrote.String(), want) {
					t.Errorf("missing command %q in %q", want, p.wrote.String())
				}
			}
			if strings.ContainsAny(p.wrote.String(), "XYZW") {
				t.Fatal("logic capture programmed an output generator")
			}
		}
	}
}

func TestLogicCaptureRate(t *testing.T) {
	for _, tc := range []struct {
		rate logic.Frequency
		want logic.Frequency
	}{
		{4 * logic.MegaHertz, 4 * logic.MegaHertz},
		{3 * logic.MegaHertz, 2_857_142_857 * logic.MilliHertz},
		{7 * logic.MegaHertz, 6_666_666_667 * logic.MilliHertz},
		{2_441_407 * logic.MilliHertz, 2_441_406 * logic.MilliHertz},
	} {
		c := validLogicConfig()
		c.Rate, c.Samples, c.PreTrigger = tc.rate, 32, 0
		p := &testPort{reply: func(cmd string) string {
			switch cmd {
			case "7c@p":
				return cmd + "\r00\r"
			case "D":
				return cmd + "02\r1234\r00\r2345\r000010\r"
			case "A":
				return cmd + string(make([]byte, c.Samples))
			default:
				return cmd
			}
		}}
		s := &BitScope{vm: vm{port: p, timeout: time.Second}}
		tr, err := s.captureLogic(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Rate != tc.want {
			t.Errorf("requested %v: got %v, want %v", tc.rate, tr.Rate, tc.want)
		}
	}
}

func TestLogicCaptureErrors(t *testing.T) {
	c := validLogicConfig()
	c.Channels, c.Source = 0x10, 4
	p := &testPort{reply: func(cmd string) string { return cmd + "\r40\r" }}
	s := &BitScope{vm: vm{port: p, timeout: time.Millisecond}}
	if _, err := s.captureLogic(context.Background(), c); err == nil {
		t.Fatal("AWG pin conflict was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.captureLogic(ctx, validLogicConfig()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context: %v", err)
	}
	c = validLogicConfig()
	c.Channels = 0
	p.wrote.Reset()
	if _, err := s.captureLogic(context.Background(), c); err == nil || p.wrote.Len() != 0 {
		t.Fatal("invalid configuration accessed device")
	}
}

func TestLogicCancellation(t *testing.T) {
	p := &testPort{reply: func(cmd string) string {
		switch cmd {
		case "7c@p":
			return cmd + "\r00\r"
		case "D":
			return cmd + "02\r1234\r"
		case "K?":
			return "03\r1234\r00000020\rK?\rBS000500\r"
		default:
			return cmd
		}
	}}
	s := &BitScope{name: "BS000500", vm: vm{port: p, timeout: time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := s.captureLogic(ctx, validLogicConfig()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel logic trace: %v", err)
	}
	if !strings.HasSuffix(p.wrote.String(), "K?") || strings.Contains(p.wrote.String(), "!") {
		t.Fatal("cancellation did not preserve VM state")
	}
	if err := s.vm.issue("37@00s"); err != nil {
		t.Fatalf("link not synchronized after cancellation: %v", err)
	}
}

func TestLogicExport(t *testing.T) {
	dev := logic.NewDevice(logic.ClassBitScope)
	tr := &logic.Trace{Rate: 4 * logic.MegaHertz, Inputs: dev.TraceInputs(3), Trigger: dev.ResourceID(logic.KindDigitalIn, 1), TriggerAt: 1, Triggered: true, Samples: []byte{0, 1, 3, 2}}
	var b bytes.Buffer
	if err := logic.Write(&b, "json", tr); err != nil {
		t.Fatal(err)
	}
	var decoded logic.Trace
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil || !bytes.Equal(tr.Samples, decoded.Samples) || decoded.Rate != 4*logic.MegaHertz {
		t.Fatalf("JSON round trip: %+v %v", decoded, err)
	}
	b.Reset()
	if err := logic.Write(&b, "csv", tr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "# trigger_sample=1\n") || !strings.Contains(b.String(), "# triggered=true\n") {
		t.Fatal("CSV has no trigger metadata")
	}
	r := csv.NewReader(&b)
	r.Comment = '#'
	rows, err := r.ReadAll()
	if err != nil || len(rows) != 5 || strings.Join(rows[0], ",") != "time_seconds,L0,L1" ||
		strings.Join(rows[2], ",") != "2.5e-07,1,0" {
		t.Fatalf("CSV: %v %v", rows, err)
	}
	b.Reset()
	if err := logic.Write(&b, "vcd", tr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"trigger_sample=1 triggered=true", "$timescale 1 ps $end", "$var wire 1 \" L1 $end",
		"#0\n$dumpvars\n0!\n0\"\n$end\n#250000\n1!\n#500000\n1\"\n#750000\n0!\n#1000000\n",
	} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("VCD has no %q:\n%s", want, b.String())
		}
	}
	if err := logic.Write(io.Discard, "xml", tr); err == nil {
		t.Fatal("unknown format accepted")
	}
	if err := logic.Write(io.Discard, "csv", nil); err == nil {
		t.Fatal("missing trace accepted")
	}
	if err := logic.Write(failingWriter{}, "csv", tr); err == nil {
		t.Fatal("lost output error")
	}
	if err := logic.Write(failingWriter{}, "vcd", tr); err == nil {
		t.Fatal("lost VCD output error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestLogicDevice(t *testing.T) {
	port := os.Getenv("BITSCOPE_LOGIC_PORT")
	if port == "" {
		t.Skip("set BITSCOPE_LOGIC_PORT to check digital transfer")
	}
	dev, err := Open(port)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	c := validLogicConfig()
	c.Channels = 0x3f
	c.Auto = 50 * time.Millisecond
	for range 5 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		tr, err := dev.CaptureLogic(ctx, capturetest.Request(dev, c))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if tr.Rate != 4*logic.MegaHertz || len(tr.Samples) != c.Samples {
			t.Fatalf("unexpected capture: %+v", tr)
		}
		t.Logf("%d samples at %g S/s, trigger=%t", len(tr.Samples), tr.Rate.Hz(), tr.Triggered)
	}
}

func TestBitScopeCapabilities(t *testing.T) {
	dev := logic.NewDevice(logic.ClassBitScope)
	s := (&BitScope{device: dev}).Capabilities()
	var l4, awg logic.ResourceID
	for r := range s.Resources() {
		if r.ID.Device() != dev {
			t.Fatalf("%s: ID %+v, want device %+v", r.Name, r.ID, dev)
		}
		switch {
		case r.Kind.Has(logic.KindDigitalIn):
			// The suggested rates are exact; MinHz and MaxHz are the limits.
			for _, rate := range r.Rates {
				ticks, _, _, err := bitscopeLogicPlan(capture.Config{Channels: 1, Rate: rate, Samples: 32})
				if err != nil || logic.FrequencyFromHz(clockRate/float64(ticks)) != rate {
					t.Errorf("%s: rate %v: ticks %d, err %v", r.Name, rate, ticks, err)
				}
			}
			for rate, ok := range map[logic.Frequency]bool{r.MinHz: true, r.MaxHz: true, r.MinHz - 1: false, r.MaxHz + 1: false} {
				if err := (&BitScope{}).validateLogic(capture.Config{Channels: 1, Rate: rate, Samples: 32}); (err == nil) != ok {
					t.Errorf("%s: rate %v: err %v", r.Name, rate, err)
				}
			}
			if r.Name == "L4" {
				l4 = r.ID
			}
		case r.Kind.Has(logic.KindAnalogIn):
			if r.MaxHz != 20*logic.MegaHertz || len(r.Rates) != 0 {
				t.Errorf("%s: max %v, rates %v", r.Name, r.MaxHz, r.Rates)
			}
		case r.Kind.Has(logic.KindAWG):
			awg = r.ID
			if r.MinHz != 10*logic.Hertz || r.MaxHz != 10*logic.KiloHertz || len(r.Waveforms) != 4 {
				t.Errorf("awg: %+v", r)
			}
		}
	}
	for _, k := range []logic.RelationKind{logic.RelationAlias, logic.RelationConflict} {
		if !s.HasRelation(awg, l4, k) {
			t.Errorf("no relation %d between L4 and the AWG", k)
		}
	}
}
