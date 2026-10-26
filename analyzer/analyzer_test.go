package analyzer

import (
	"slices"
	"testing"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/bitscope"
	"github.com/davecheney/logic/dslogic"
	"github.com/davecheney/logic/fx2lafw"
	"github.com/davecheney/logic/ols"
)

var (
	_ logic.Analyzer = (*bitscope.BitScope)(nil)
	_ logic.Analyzer = (*dslogic.DSLogic)(nil)
	_ logic.Analyzer = (*fx2lafw.FX2)(nil)
	_ logic.Analyzer = (*ols.OLS)(nil)
)

func TestCapabilityInvariants(t *testing.T) {
	for _, d := range drivers {
		t.Run(d.name, func(t *testing.T) {
			dev := logic.NewDevice(d.class)
			s := d.capabilities(dev)
			if len(slices.Collect(s.Resources())) == 0 {
				t.Fatal("no resources")
			}
			ids := map[logic.ResourceID]bool{}
			physical := map[logic.PhysicalPin]logic.ResourceID{}
			for r := range s.Resources() {
				switch {
				case ids[r.ID]:
					t.Errorf("%s: duplicate ID %+v", r.Name, r.ID)
				case r.ID.Device() != dev:
					t.Errorf("%s: ID %+v, want device %+v", r.Name, r.ID, dev)
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
				if r.Physical.Device() != dev {
					t.Errorf("%s: pin %v, want device %+v", r.Name, r.Physical, dev)
				}
				if other, ok := physical[r.Physical]; ok && !s.HasRelation(r.ID, other, logic.RelationAlias) {
					t.Errorf("%s: shares physical %v with %+v without an alias", r.Name, r.Physical, other)
				}
				physical[r.Physical] = r.ID
			}
			for rel := range s.Relations() {
				a := s.Resource(rel.A)
				b := s.Resource(rel.B)
				if a == nil || b == nil {
					t.Errorf("relation %+v names an unknown resource", rel)
				} else if rel.Kind == logic.RelationAlias && a.Physical != b.Physical {
					t.Errorf("alias %s and %s use different pins", a.Name, b.Name)
				}
			}
		})
	}
}

func TestCapabilityDevices(t *testing.T) {
	for _, d := range drivers {
		sa, sb := d.capabilities(logic.NewDevice(d.class)), d.capabilities(logic.NewDevice(d.class))
		a, b := slices.Collect(sa.Resources()), slices.Collect(sb.Resources())
		for i := range a {
			ra, rb := a[i].ID, b[i].ID
			if ra == rb || ra.Device().Class() != rb.Device().Class() || ra.Kind() != rb.Kind() || ra.Index() != rb.Index() {
				t.Errorf("%s: IDs %+v and %+v", d.name, ra, rb)
			}
		}
		// Each resource has its own rate list.
		if len(a[0].Rates) > 0 {
			a[0].Rates[0] = 0
			if a[1].Rates[0] == 0 || b[0].Rates[0] == 0 {
				t.Errorf("%s: resources share a rate list", d.name)
			}
		}
	}
}

func TestCapabilitiesFor(t *testing.T) {
	for _, name := range Devices() {
		caps, err := CapabilitiesFor(name)
		if err != nil {
			t.Fatal(err)
		}
		if len(slices.Collect(caps.Resources())) == 0 {
			t.Errorf("%s: no resources", name)
		}
	}
	if _, err := CapabilitiesFor("nope"); err == nil {
		t.Fatal("unknown device accepted")
	}
}

func TestCapabilitySampleLimits(t *testing.T) {
	tests := []struct {
		name       string
		device     string
		inputs     int
		want       int
		wantExists bool
	}{
		{"BitScope one input", "bitscope", 1, 12288, true},
		{"BitScope six inputs", "bitscope", 6, 12288, true},
		{"BitScope seven inputs", "bitscope", 7, 0, false},
		{"DSLogic four inputs", "dslogic", 4, 67_108_864, true},
		{"DSLogic sixteen inputs", "dslogic", 16, 16_777_216, true},
		{"DSLogic no inputs", "dslogic", 0, 0, false},
		{"FX2 has no reported limit", "fx2lafw", 8, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps, err := CapabilitiesFor(tt.device)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := caps.MaxSamplesFor(tt.inputs)
			if got != tt.want || ok != tt.wantExists {
				t.Fatalf("MaxSamplesFor(%d) = %d, %t; want %d, %t",
					tt.inputs, got, ok, tt.want, tt.wantExists)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	caps, err := CapabilitiesFor("fx2lafw")
	if err != nil {
		t.Fatal(err)
	}
	l0 := slices.Collect(caps.Resources())[0].ID
	req := logic.CaptureRequest{Inputs: []logic.ResourceID{l0}, Trigger: l0, Rate: logic.MegaHertz, Samples: 4096}
	if err := Validate("fx2lafw", req); err != nil {
		t.Fatal(err)
	}
	if err := Validate("saleae", req); err != nil {
		t.Fatal(err)
	}
	if err := Validate("dslogic", req); err == nil {
		t.Fatal("request for the wrong device class accepted")
	}
	if err := Validate("nope", req); err == nil {
		t.Fatal("unknown device accepted")
	}
}
