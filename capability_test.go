package logic

import (
	"slices"
	"testing"
)

// testCapabilities returns a small capability set with four inputs.
func testCapabilities(dev *Device) CapabilitySet {
	return NewCapabilitySet(
		DigitalInputs(dev, 4, KiloHertz, MegaHertz, []Frequency{MegaHertz, KiloHertz}),
		nil,
		[]SampleLimit{{InputCount: 1, MaxSamples: 100}, {InputCount: 2, MaxSamples: 50}},
	)
}

func TestResourceKindHas(t *testing.T) {
	k := KindDigitalIn | KindTrigger
	if !k.Has(KindDigitalIn) || !k.Has(KindTrigger|KindAWG) || k.Has(KindAWG|KindClockOut) {
		t.Fatalf("Has: wrong result for %b", k)
	}
}

func TestCapabilityResources(t *testing.T) {
	caps := testCapabilities(NewDevice(ClassFX2))
	for range caps.Resources() {
		break
	}
	for range 2 {
		count := 0
		for r := range caps.Resources() {
			if r.ID != caps.resources[count].ID {
				t.Fatalf("resource %d: got %+v, want %+v", count, r.ID, caps.resources[count].ID)
			}
			count++
		}
		if count != len(caps.resources) {
			t.Fatalf("got %d resources, want %d", count, len(caps.resources))
		}
	}
}

func TestCapabilityResourceLookup(t *testing.T) {
	dev := NewDevice(ClassFX2)
	caps := testCapabilities(dev)
	if r := caps.Resource(caps.resources[0].ID); r == nil || r.Name != "L0" {
		t.Fatalf("Resource(L0) = %v", r)
	}
	if r := caps.Resource(dev.ResourceID(KindDigitalIn, 15)); r != nil {
		t.Fatalf("Resource(unknown) = %v, want nil", r)
	}
}

func TestNewDevice(t *testing.T) {
	ids := map[uint64]bool{}
	for _, class := range []DeviceClass{ClassBitScope, ClassBitScope, ClassDSLogic, ClassFX2} {
		d := NewDevice(class)
		if d.class != class || d.id == 0 || ids[d.id] {
			t.Fatalf("NewDevice(%v): %+v", class, d)
		}
		ids[d.id] = true
	}
}

func TestPhysicalPin(t *testing.T) {
	// The same pin number on another class or device is another pin.
	a := NewDevice(ClassBitScope).PhysicalPin(0)
	for _, b := range []PhysicalPin{NewDevice(ClassFX2).PhysicalPin(0), NewDevice(ClassBitScope).PhysicalPin(0)} {
		if a == b {
			t.Errorf("%v == %v", a, b)
		}
	}
	pin := (&Device{id: 3, class: ClassFX2}).PhysicalPin(7)
	if got := pin.Pin(); got != 7 {
		t.Errorf("Pin: %d, want 7", got)
	}
	if got := pin.String(); got != "fx2lafw/3/7" {
		t.Errorf("String: %q", got)
	}
}

func TestCapabilitySampleLimitsIterator(t *testing.T) {
	caps := testCapabilities(NewDevice(ClassFX2))
	for range caps.SampleLimits() {
		break
	}
	for range 2 {
		got := slices.Collect(caps.SampleLimits())
		if !slices.Equal(got, caps.sampleLimits) {
			t.Fatalf("SampleLimits() = %v, want %v", got, caps.sampleLimits)
		}
	}
	var empty CapabilitySet
	if got := slices.Collect(empty.SampleLimits()); len(got) != 0 {
		t.Fatalf("empty SampleLimits() = %v", got)
	}
}

func TestCapabilityMaxSamplesFor(t *testing.T) {
	caps := testCapabilities(NewDevice(ClassFX2))
	for inputs, want := range map[int]struct {
		n  int
		ok bool
	}{0: {0, false}, 1: {100, true}, 2: {50, true}, 3: {0, false}} {
		n, ok := caps.MaxSamplesFor(inputs)
		if n != want.n || ok != want.ok {
			t.Errorf("MaxSamplesFor(%d) = %d, %t; want %d, %t", inputs, n, ok, want.n, want.ok)
		}
	}
}
