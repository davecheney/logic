package logic

import (
	"cmp"
	"slices"
	"testing"
)

var (
	_ Analyzer = (*BitScope)(nil)
	_ Analyzer = (*DSLogic)(nil)
	_ Analyzer = (*FX2)(nil)
)

func TestResourceKindHas(t *testing.T) {
	k := KindDigitalIn | KindTrigger
	if !k.Has(KindDigitalIn) || !k.Has(KindTrigger|KindAWG) || k.Has(KindAWG|KindClockOut) {
		t.Fatalf("Has: wrong result for %b", k)
	}
}

var capabilityTables = []struct {
	class DeviceClass
	table func(*device) CapabilitySet
}{
	{ClassBitScope, bitscopeCapabilities},
	{ClassDSLogic, dslogicCapabilities},
	{ClassFX2, fx2Capabilities},
}

func TestCapabilityInvariants(t *testing.T) {
	for _, tc := range capabilityTables {
		t.Run(tc.class.String(), func(t *testing.T) {
			dev := newDevice(tc.class)
			s := tc.table(dev)
			if len(s.Resources) == 0 {
				t.Fatal("no resources")
			}
			ids := map[ResourceID]bool{}
			physical := map[PhysicalPin]ResourceID{}
			for _, r := range s.Resources {
				switch {
				case ids[r.ID]:
					t.Errorf("%s: duplicate ID %+v", r.Name, r.ID)
				case r.ID.Device() != dev || r.ID.Device().Class() != dev.class:
					t.Errorf("%s: ID %+v, want device %d class %v", r.Name, r.ID, dev.id, dev.class)
				case !r.Kind.Has(r.ID.Kind()) || r.ID.Kind()&(r.ID.Kind()-1) != 0:
					t.Errorf("%s: ID kind %b is not one role of %b", r.Name, r.ID.Kind(), r.Kind)
				case r.Name == "":
					t.Errorf("%+v: no name", r.ID)
				case r.MinHz == 0 || r.MinHz > r.MaxHz:
					t.Errorf("%s: range %v to %v", r.Name, r.MinHz, r.MaxHz)
				}
				ids[r.ID] = true
				if len(r.Rates) > 0 {
					if r.Rates[0] != r.MaxHz {
						t.Errorf("%s: first rate %v, want MaxHz %v", r.Name, r.Rates[0], r.MaxHz)
					}
					for i, f := range r.Rates {
						if f < r.MinHz || f > r.MaxHz || i > 0 && f >= r.Rates[i-1] {
							t.Errorf("%s: rate %v is out of range or order", r.Name, f)
						}
					}
				}
				if other, ok := physical[r.Physical]; ok && !s.HasRelation(r.ID, other, RelationAlias) {
					t.Errorf("%s: shares physical %#x with %+v without an alias", r.Name, r.Physical, other)
				}
				physical[r.Physical] = r.ID
			}
			for _, rel := range s.Relations {
				a, okA := s.Resource(rel.A)
				b, okB := s.Resource(rel.B)
				if !okA || !okB {
					t.Errorf("relation %+v names an unknown resource", rel)
				} else if rel.Kind == RelationAlias && a.Physical != b.Physical {
					t.Errorf("alias %s and %s use different pins", a.Name, b.Name)
				}
			}
		})
	}
}

func TestCapabilityDevices(t *testing.T) {
	for _, tc := range capabilityTables {
		a, b := tc.table(newDevice(tc.class)), tc.table(newDevice(tc.class))
		for i := range a.Resources {
			ra, rb := a.Resources[i].ID, b.Resources[i].ID
			if ra == rb || ra.Device().Class() != rb.Device().Class() || ra.Kind() != rb.Kind() || ra.Index() != rb.Index() {
				t.Errorf("%v: IDs %+v and %+v", tc.class, ra, rb)
			}
		}
		// Each resource has its own rate list.
		if len(a.Resources[0].Rates) > 0 {
			a.Resources[0].Rates[0] = 0
			if a.Resources[1].Rates[0] == 0 || b.Resources[0].Rates[0] == 0 {
				t.Errorf("%v: resources share a rate list", tc.class)
			}
		}
	}
	for _, tc := range []struct {
		dev      *device
		analyzer func(*device) Analyzer
	}{
		{newDevice(ClassBitScope), func(d *device) Analyzer { return &BitScope{device: d} }},
		{newDevice(ClassDSLogic), func(d *device) Analyzer { return &DSLogic{device: d} }},
		{newDevice(ClassFX2), func(d *device) Analyzer { return &FX2{device: d} }},
	} {
		a := tc.analyzer(tc.dev)
		for _, r := range a.Capabilities().Resources {
			if r.ID.Device() != tc.dev || r.ID.Device().Class() != tc.dev.class {
				t.Fatalf("%T: resource %s has ID %+v, want device %+v", a, r.Name, r.ID, tc.dev)
			}
		}
	}
}

func TestNewDevice(t *testing.T) {
	ids := map[uint64]bool{}
	for _, class := range []DeviceClass{ClassBitScope, ClassBitScope, ClassDSLogic, ClassFX2} {
		d := newDevice(class)
		if d.class != class || d.id == 0 || ids[d.id] {
			t.Fatalf("newDevice(%v): %+v", class, d)
		}
		ids[d.id] = true
	}
}

func TestCapabilityPinNamespace(t *testing.T) {
	for _, tc := range capabilityTables {
		for range 2 {
			dev := newDevice(tc.class)
			for _, r := range tc.table(dev).Resources {
				if r.Physical.Device().Class() != dev.class || r.Physical.Device() != dev {
					t.Errorf("%v %s: pin %v, want class %v device %d", tc.class, r.Name, r.Physical, dev.class, dev.id)
				}
			}
		}
	}
	// The same pin number on another class or device is another pin.
	a := newDevice(ClassBitScope).physicalPin(0)
	for _, b := range []PhysicalPin{newDevice(ClassFX2).physicalPin(0), newDevice(ClassBitScope).physicalPin(0)} {
		if a == b {
			t.Errorf("%v == %v", a, b)
		}
	}
	pin := (&device{id: 3, class: ClassFX2}).physicalPin(7)
	if got := pin.Pin(); got != 7 {
		t.Errorf("Pin: %d, want 7", got)
	}
	if got := pin.String(); got != "fx2lafw/3/7" {
		t.Errorf("String: %q", got)
	}
}

func TestBitScopeCapabilities(t *testing.T) {
	s := bitscopeCapabilities(newDevice(ClassBitScope))
	var l4, awg ResourceID
	for _, r := range s.Resources {
		switch {
		case r.Kind.Has(KindDigitalIn):
			// The suggested rates are exact; MinHz and MaxHz are the limits.
			for _, rate := range r.Rates {
				ticks, _, _, err := bitscopeLogicPlan(Config{Channels: 1, Rate: rate, Samples: 32})
				if err != nil || frequencyFromHz(clockRate/float64(ticks)) != rate {
					t.Errorf("%s: rate %v: ticks %d, err %v", r.Name, rate, ticks, err)
				}
			}
			for rate, ok := range map[Frequency]bool{r.MinHz: true, r.MaxHz: true, r.MinHz - 1: false, r.MaxHz + 1: false} {
				if err := (&BitScope{}).ValidateLogic(Config{Channels: 1, Rate: rate, Samples: 32}); (err == nil) != ok {
					t.Errorf("%s: rate %v: err %v", r.Name, rate, err)
				}
			}
			if r.Name == "L4" {
				l4 = r.ID
			}
		case r.Kind.Has(KindAnalogIn):
			if r.MaxHz != 20*MegaHertz || len(r.Rates) != 0 {
				t.Errorf("%s: max %v, rates %v", r.Name, r.MaxHz, r.Rates)
			}
		case r.Kind.Has(KindAWG):
			awg = r.ID
			if r.MinHz != 10*Hertz || r.MaxHz != 10*KiloHertz || len(r.Waveforms) != 4 {
				t.Errorf("awg: %+v", r)
			}
		}
	}
	for _, k := range []RelationKind{RelationAlias, RelationConflict} {
		if !s.HasRelation(awg, l4, k) {
			t.Errorf("no relation %d between L4 and the AWG", k)
		}
	}
}

func TestDSLogicCapabilities(t *testing.T) {
	s := dslogicCapabilities(newDevice(ClassDSLogic))
	if len(s.Resources) != 16 {
		t.Fatalf("%d resources, want 16", len(s.Resources))
	}
	r := s.Resources[0]
	if r.MinHz != r.Rates[len(r.Rates)-1] {
		t.Errorf("last rate %v, want MinHz %v", r.Rates[len(r.Rates)-1], r.MinHz)
	}
	for _, rate := range r.Rates {
		p, err := dslogicPlan(Config{Channels: 1, Rate: rate, Samples: 4096})
		if err != nil || frequencyFromHz(p.rate) != rate {
			t.Errorf("rate %v: err %v", rate, err)
		}
	}
	for _, rate := range []Frequency{r.MinHz - 1, r.MaxHz + 1} {
		if _, err := dslogicPlan(Config{Channels: 1, Rate: rate, Samples: 4096}); err == nil {
			t.Errorf("rate %v: no error", rate)
		}
	}
	// Every input shows the 400 MHz maximum. The driver rejects inputs
	// that the fast modes cannot use.
	for _, c := range []Config{
		{Channels: 1 << 4, Rate: 400 * MegaHertz, Source: 4, Samples: 4096},
		{Channels: 1 << 8, Rate: 200 * MegaHertz, Source: 8, Samples: 4096},
	} {
		if s.Resources[c.Source].MaxHz < c.Rate {
			t.Errorf("L%d: MaxHz %v", c.Source, s.Resources[c.Source].MaxHz)
		}
		if err := (&DSLogic{}).ValidateLogic(c); err == nil {
			t.Errorf("L%d at %v: no error", c.Source, c.Rate)
		}
	}
}

func TestFX2Capabilities(t *testing.T) {
	s := fx2Capabilities(newDevice(ClassFX2))
	if len(s.Resources) != 8 {
		t.Fatalf("%d resources, want 8", len(s.Resources))
	}
	r := s.Resources[0]
	if r.MinHz != 20*KiloHertz || r.MaxHz != 24*MegaHertz {
		t.Fatalf("range %v to %v", r.MinHz, r.MaxHz)
	}
	// A short selection, fastest first, from the maximum to the minimum.
	if len(r.Rates) >= len(fx2Rates) || r.Rates[0] != r.MaxHz || r.Rates[len(r.Rates)-1] != r.MinHz ||
		!slices.IsSortedFunc(r.Rates, func(a, b Frequency) int { return cmp.Compare(b, a) }) {
		t.Fatalf("rates %v", r.Rates)
	}
	for _, rate := range r.Rates {
		if !slices.Contains(fx2Rates, rate) {
			t.Errorf("rate %v is not a firmware rate", rate)
		}
		if err := (&FX2{}).ValidateLogic(Config{Channels: 1, Rate: rate, Samples: 2}); err != nil {
			t.Errorf("rate %v: %v", rate, err)
		}
	}
}
