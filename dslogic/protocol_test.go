package dslogic

import (
	"encoding/binary"
	"slices"
	"testing"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
)

func TestSettingLayout(t *testing.T) {
	p, err := dslogicPlan(capture.Config{Channels: 1, Rate: 1 * logic.MegaHertz, Samples: 4096})
	if err != nil {
		t.Fatal(err)
	}
	b := p.set.bytes()
	if len(b) != 372 {
		t.Fatalf("setting is %d bytes, want 372", len(b))
	}
	u16 := func(off int) uint16 { return binary.LittleEndian.Uint16(b[off:]) }
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
	for _, tc := range []struct {
		name string
		got  uint32
		want uint32
	}{
		{"sync", u32(0), 0xf5a5f5a5},
		{"mode header", uint32(u16(4)), 0x0001},
		{"trig header", uint32(u16(46)), 0x40a0},
		{"trig_mask0[0]", uint32(u16(48)), 0xfffe},
		{"trig_count[0]", u32(304), 0},
		{"end sync", u32(368), 0xfa5afa5a},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %#x, want %#x", tc.name, tc.got, tc.want)
		}
	}
}

func TestPlan(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       capture.Config
		rate      float64
		traceRate logic.Frequency
		mode      uint16
		div       uint32
		cnt       uint32
		tpos      uint32
		mask0     uint16
		value0    uint16
		edge0     uint16
		enabled   int
		trigGlb   uint16
		actual    int
		dsoCount  uint32
	}{{
		name: "1 MHz, 16 inputs, rising L0",
		cfg:  capture.Config{Channels: 0xffff, Rate: 1 * logic.MegaHertz, Samples: 4096, PreTrigger: 0.05},
		rate: 1e6, traceRate: logic.MegaHertz, mode: modeTrigEn, div: 100,
		cnt: 4096 >> 4, tpos: 192,
		mask0: 0xfffe, value0: 1, edge0: 1,
		enabled: 16, trigGlb: 16 << 8, actual: 4096, dsoCount: 4096,
	}, {
		name: "rounds the rate down and the count up",
		cfg:  capture.Config{Channels: 0x0003, Rate: 3 * logic.MegaHertz, Samples: 1000, Source: 1, Falling: true},
		rate: 100e6 / 34, traceRate: 2_941_176_471 * logic.MilliHertz, mode: modeTrigEn | modeSlow, div: 34,
		cnt: 1024 >> 4, tpos: 64,
		mask0: 0xfffd, value0: 0, edge0: 2,
		enabled: 2, trigGlb: 2 << 8, actual: 1024, dsoCount: 1024,
	}, {
		name: "100 MHz",
		cfg:  capture.Config{Channels: 0x00ff, Rate: 100 * logic.MegaHertz, Samples: 1 << 20, PreTrigger: 0.5},
		rate: 100e6, traceRate: 100 * logic.MegaHertz, mode: modeTrigEn, div: 1,
		cnt: 1 << 16, tpos: 1 << 19,
		mask0: 0xfffe, value0: 1, edge0: 1,
		enabled: 8, trigGlb: 8 << 8, actual: 1 << 20, dsoCount: 1 << 20,
	}, {
		name: "200 MHz copies the trigger to both bytes",
		cfg:  capture.Config{Channels: 0x0081, Rate: 200 * logic.MegaHertz, Samples: 4096, Source: 7},
		rate: 200e6, traceRate: 200 * logic.MegaHertz, mode: modeTrigEn | modeHalf, div: 1,
		cnt: 4096 >> 4, tpos: 64,
		mask0: 0x7f7f, value0: 0x8080, edge0: 0x8080,
		enabled: 2, trigGlb: 2 << 8, actual: 4096, dsoCount: 4096,
	}, {
		name: "400 MHz copies the trigger to all nibbles",
		cfg:  capture.Config{Channels: 0x000f, Rate: 400 * logic.MegaHertz, Samples: 4096, Source: 2},
		rate: 400e6, traceRate: 400 * logic.MegaHertz, mode: modeTrigEn | modeQuar, div: 1,
		cnt: 4096 >> 4, tpos: 64,
		mask0: 0xbbbb, value0: 0x4444, edge0: 0x4444,
		enabled: 4, trigGlb: 4 << 8, actual: 4096, dsoCount: 4096,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := dslogicPlan(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			s := p.set
			check := func(name string, got, want uint32) {
				t.Helper()
				if got != want {
					t.Errorf("%s: got %#x, want %#x", name, got, want)
				}
			}
			if p.rate != tc.rate {
				t.Errorf("rate: got %g, want %g", p.rate, tc.rate)
			}
			if got := logic.FrequencyFromHz(p.rate); got != tc.traceRate {
				t.Errorf("trace rate: got %v, want %v", got, tc.traceRate)
			}
			check("mode", uint32(s.Mode), uint32(tc.mode))
			check("div", uint32(s.DivH)<<16|uint32(s.DivL), tc.div)
			check("cnt", uint32(s.CntH)<<16|uint32(s.CntL), tc.cnt)
			check("dso cnt", uint32(s.DSOCntH)<<16|uint32(s.DSOCntL), tc.dsoCount)
			check("tpos", uint32(s.TposH)<<16|uint32(s.TposL), tc.tpos)
			check("trig glb", uint32(s.TrigGlb), uint32(tc.trigGlb))
			check("ch en", uint32(s.ChEnL), uint32(tc.cfg.Channels))
			check("mask0", uint32(s.TrigMask0[0]), uint32(tc.mask0))
			check("value0", uint32(s.TrigValue0[0]), uint32(tc.value0))
			check("edge0", uint32(s.TrigEdge0[0]), uint32(tc.edge0))
			check("mask1", uint32(s.TrigMask1[0]), 0xffff)
			check("actual", uint32(p.actual), uint32(tc.actual))
			check("enabled", uint32(len(p.enabled)), uint32(tc.enabled))
			for i := 1; i < numStages; i++ {
				if s.TrigMask0[i] != 0xffff || s.TrigMask1[i] != 0xffff {
					t.Errorf("stage %d is not masked", i)
				}
			}
			for i := range numStages {
				if s.TrigLogic0[i] != 2 || s.TrigLogic1[i] != 2 {
					t.Errorf("stage %d logic: %d, %d", i, s.TrigLogic0[i], s.TrigLogic1[i])
				}
			}
		})
	}
}

func TestPlanErrors(t *testing.T) {
	ok := capture.Config{Channels: 1, Rate: 1 * logic.MegaHertz, Samples: 4096}
	for name, change := range map[string]func(*capture.Config){
		"no inputs":          func(c *capture.Config) { c.Channels = 0 },
		"rate too low":       func(c *capture.Config) { c.Rate = 10 * logic.KiloHertz },
		"rate too high":      func(c *capture.Config) { c.Rate = 500 * logic.MegaHertz },
		"400 MHz with L4":    func(c *capture.Config) { c.Rate, c.Channels = 400*logic.MegaHertz, 0x11 },
		"200 MHz with L8":    func(c *capture.Config) { c.Rate, c.Channels = 200*logic.MegaHertz, 0x101 },
		"trigger not on":     func(c *capture.Config) { c.Source = 3 },
		"trigger out of set": func(c *capture.Config) { c.Source = 16 },
		"too few samples":    func(c *capture.Config) { c.Samples = 10 },
		"too many samples":   func(c *capture.Config) { c.Channels, c.Samples = 0xffff, 1<<25 },
		"pre-trigger":        func(c *capture.Config) { c.PreTrigger = 0.95 },
		"negative auto":      func(c *capture.Config) { c.Auto = -time.Second },
	} {
		c := ok
		change(&c)
		if _, err := dslogicPlan(c); err == nil {
			t.Errorf("%s: plan accepted %+v", name, c)
		}
	}
	if _, err := dslogicPlan(ok); err != nil {
		t.Fatal(err)
	}
}

func TestHeader(t *testing.T) {
	b := make([]byte, headerSize)
	for i, v := range []uint32{trigCheckID, 254, 0x320c0, 3903, 0, 1} {
		binary.LittleEndian.PutUint32(b[4*i:], v)
	}
	h, err := parseHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if h.RealPos != 254 || h.Status != 1 {
		t.Errorf("header %+v", h)
	}
	p, _ := dslogicPlan(capture.Config{Channels: 1, Rate: 1 * logic.MegaHertz, Samples: 4096})
	if got := h.samples(p); got != 0 {
		t.Errorf("samples with remain 3903: got %d, want 0", got)
	}
	h.RemainL = 0
	if got := h.samples(p); got != 4096 {
		t.Errorf("samples with remain 0: got %d, want 4096", got)
	}
	h.RemainL = 2000
	if got := h.samples(p); got != 2048 {
		t.Errorf("samples with remain 2000: got %d, want 2048", got)
	}
	h.RemainL = 5000
	if got := h.samples(p); got != 0 {
		t.Errorf("samples with remain 5000: got %d, want 0", got)
	}
	p, _ = dslogicPlan(capture.Config{Channels: 1, Rate: 1 * logic.MegaHertz, Samples: 1000})
	h.RemainL = 0
	if got := h.samples(p); got != 1024 {
		t.Errorf("samples for a 1000-sample request: got %d, want 1024", got)
	}

	b[0] = 0
	if _, err := parseHeader(b); err == nil {
		t.Error("parseHeader accepted a bad check ID")
	}
	if _, err := parseHeader(b[:8]); err == nil {
		t.Error("parseHeader accepted a short header")
	}
}

func TestTranspose(t *testing.T) {
	enabled := []int{0, 3, 15}
	const blocks = 2
	src := make([]byte, blocks*len(enabled)*8)
	want := make([]uint16, blocks*atomic)
	for i := range want {
		want[i] = uint16(i) * 0x9e37 & (1<<0 | 1<<3 | 1<<15)
	}
	for b := range blocks {
		for i, ch := range enabled {
			var w uint64
			for k := range atomic {
				if want[b*atomic+k]&(1<<ch) != 0 {
					w |= 1 << k
				}
			}
			binary.LittleEndian.PutUint64(src[(b*len(enabled)+i)*8:], w)
		}
	}
	if n := dataBytes(blocks*atomic, len(enabled)); n != len(src) {
		t.Fatalf("dataBytes: got %d, want %d", n, len(src))
	}
	dst := make([]byte, 2*len(want))
	transpose(dst, src, enabled)
	tr := logic.Trace{Width: 2, Samples: dst}
	for i, w := range want {
		if got := tr.At(i); got != uint64(w) {
			t.Fatalf("sample %d: got %#04x, want %#04x", i, got, w)
		}
	}
}

func TestDSLogicCapabilities(t *testing.T) {
	dev := logic.NewDevice(logic.ClassDSLogic)
	s := (&DSLogic{device: dev}).Capabilities()
	rs := slices.Collect(s.Resources())
	if len(rs) != 16 {
		t.Fatalf("%d resources, want 16", len(rs))
	}
	for _, r := range rs {
		if r.ID.Device() != dev {
			t.Fatalf("%s: ID %+v, want device %+v", r.Name, r.ID, dev)
		}
	}
	r := rs[0]
	if r.MinHz != r.Rates[len(r.Rates)-1] {
		t.Errorf("last rate %v, want MinHz %v", r.Rates[len(r.Rates)-1], r.MinHz)
	}
	for _, rate := range r.Rates {
		p, err := dslogicPlan(capture.Config{Channels: 1, Rate: rate, Samples: 4096})
		if err != nil || logic.FrequencyFromHz(p.rate) != rate {
			t.Errorf("rate %v: err %v", rate, err)
		}
	}
	for _, rate := range []logic.Frequency{r.MinHz - 1, r.MaxHz + 1} {
		if _, err := dslogicPlan(capture.Config{Channels: 1, Rate: rate, Samples: 4096}); err == nil {
			t.Errorf("rate %v: no error", rate)
		}
	}
	// Every input shows the 400 MHz maximum. The driver rejects inputs
	// that the fast modes cannot use.
	for _, c := range []capture.Config{
		{Channels: 1 << 4, Rate: 400 * logic.MegaHertz, Source: 4, Samples: 4096},
		{Channels: 1 << 8, Rate: 200 * logic.MegaHertz, Source: 8, Samples: 4096},
	} {
		if rs[c.Source].MaxHz < c.Rate {
			t.Errorf("L%d: MaxHz %v", c.Source, rs[c.Source].MaxHz)
		}
		if err := (&DSLogic{}).validateLogic(c); err == nil {
			t.Errorf("L%d at %v: no error", c.Source, c.Rate)
		}
	}
}
