package logic

import (
	"fmt"
	"iter"
	"math/bits"
	"slices"
	"strconv"
	syncatomic "sync/atomic"
)

var lastDeviceID syncatomic.Uint64

// Device is one analyser instance. Resource and pin IDs refer to it.
type Device struct {
	id    uint64
	class DeviceClass
}

// NewDevice returns a device of the given class with a new instance ID.
// Drivers call it once for each analyser that they open.
func NewDevice(class DeviceClass) *Device {
	return &Device{id: lastDeviceID.Add(1), class: class}
}

// Class returns the device class.
func (d *Device) Class() DeviceClass { return d.class }

// ResourceID returns the ID of a resource on d.
func (d *Device) ResourceID(kind ResourceKind, index uint) ResourceID {
	return ResourceID{device: d, kind: kind, index: index}
}

// TraceInputs returns a Trace input table for the digital inputs on d that
// mask selects. If bit n of mask is set, element n is input Ln.
func (d *Device) TraceInputs(mask uint64) []ResourceID {
	inputs := make([]ResourceID, bits.Len64(mask))
	for n := range inputs {
		if mask>>n&1 != 0 {
			inputs[n] = d.ResourceID(KindDigitalIn, uint(n))
		}
	}
	return inputs
}

// PhysicalPin returns the ID of a pin on d.
func (d *Device) PhysicalPin(pin uint) PhysicalPin {
	return PhysicalPin{device: d, pin: pin}
}

// DeviceClass identifies a kind of analyser.
//
//go:generate go tool stringer -type=DeviceClass -linecomment
type DeviceClass uint64

const (
	ClassBitScope    DeviceClass = iota + 1 // bitscope
	ClassDSLogic                            // dslogic
	ClassFX2                                // fx2lafw
	ClassOLS                                // ols
	ClassLogicPirate                        // logicpirate
)

// ResourceKind is a set of roles. A resource can have more than one role.
//
//go:generate go tool stringer -type=ResourceKind
type ResourceKind uint32

const (
	KindDigitalIn ResourceKind = 1 << iota
	KindAnalogIn
	KindDigitalOut
	KindClockOut
	KindAWG
	KindTrigger
)

// Has reports whether k has any of the roles in mask.
func (k ResourceKind) Has(mask ResourceKind) bool { return k&mask != 0 }

// ResourceID identifies a logical resource on one analyser. Device
// selects the analyser. Class, Kind, and Index group the same resource
// across analysers of one class.
type ResourceID struct {
	device *Device
	kind   ResourceKind
	index  uint
}

// Device returns the analyser that owns the resource.
func (id ResourceID) Device() *Device { return id.device }

// Kind returns the resource's role.
func (id ResourceID) Kind() ResourceKind { return id.kind }

// Index returns the resource's index on the analyser.
func (id ResourceID) Index() uint { return id.index }

// PhysicalPin identifies one pin or signal in a global namespace: device
// class, device instance, and pin. The same pin number on two classes, or
// on two analysers of one class, gives different IDs. Resources with the
// same PhysicalPin use the same hardware signal.
type PhysicalPin struct {
	device *Device
	pin    uint
}

// Device returns the analyser that owns the pin.
func (p PhysicalPin) Device() *Device { return p.device }

// Pin returns the pin number.
func (p PhysicalPin) Pin() uint { return p.pin }

func (p PhysicalPin) String() string {
	return fmt.Sprintf("%s/%d/%d", p.Device().Class(), p.device.id, p.Pin())
}

// Resource describes one logical resource.
//
// MinHz and MaxHz give the full rate range for the resource alone. Rates
// is a short list of suggested rates in that range, fastest first. It is
// not every rate the hardware accepts. Some settings limit each other,
// for example the channel mask and the rate. The table does not show
// these limits; the driver returns an error for them.
type Resource struct {
	ID        ResourceID
	Physical  PhysicalPin
	Kind      ResourceKind
	Name      string // display label only; not identity
	MinHz     Frequency
	MaxHz     Frequency
	Rates     []Frequency
	Waveforms []Waveform
}

// RelationKind is the kind of link between two resources.
type RelationKind uint8

const (
	// RelationAlias means that the resources use the same physical signal.
	RelationAlias RelationKind = iota
	// RelationConflict means that the resources cannot be used together.
	RelationConflict
)

// Relation links two resources.
type Relation struct {
	A, B ResourceID
	Kind RelationKind
}

// SampleLimit gives the maximum sample count for a number of enabled inputs.
type SampleLimit struct {
	InputCount int
	MaxSamples int
}

// CapabilitySet describes analyser resources, their relations, and capture
// limits.
type CapabilitySet struct {
	resources    []Resource
	relations    []Relation
	sampleLimits []SampleLimit
}

// NewCapabilitySet returns a capability set. Drivers use it to describe
// their resources. It does not copy the slices.
func NewCapabilitySet(resources []Resource, relations []Relation, sampleLimits []SampleLimit) CapabilitySet {
	return CapabilitySet{resources: resources, relations: relations, sampleLimits: sampleLimits}
}

// Resources returns an iterator over the resources in the capability set.
func (s *CapabilitySet) Resources() iter.Seq[Resource] {
	return slices.Values(s.resources)
}

// Relations returns an iterator over the relations between resources.
func (s *CapabilitySet) Relations() iter.Seq[Relation] {
	return slices.Values(s.relations)
}

// SampleLimits returns an iterator over the reported sample limits.
func (s *CapabilitySet) SampleLimits() iter.Seq[SampleLimit] {
	return slices.Values(s.sampleLimits)
}

// Resource returns the resource with the given ID, or nil if it is not present.
func (s *CapabilitySet) Resource(id ResourceID) *Resource {
	i := slices.IndexFunc(s.resources, func(r Resource) bool {
		return r.ID == id
	})
	if i < 0 {
		return nil
	}
	return &s.resources[i]
}

// MaxSamplesFor returns the maximum sample count for inputs, when reported.
func (s *CapabilitySet) MaxSamplesFor(inputs int) (int, bool) {
	for limit := range s.SampleLimits() {
		if limit.InputCount == inputs {
			return limit.MaxSamples, true
		}
	}
	return 0, false
}

// HasRelation reports whether a and b have a relation of the given kind,
// in either order.
func (s *CapabilitySet) HasRelation(a, b ResourceID, kind RelationKind) bool {
	for r := range s.Relations() {
		if r.Kind == kind && (r.A == a && r.B == b || r.A == b && r.B == a) {
			return true
		}
	}
	return false
}

// DigitalInputs returns n digital inputs, L0 to Ln-1, on pins 0 to n-1,
// with the same rates. Each input can also be the trigger source.
func DigitalInputs(dev *Device, n int, min, max Frequency, rates []Frequency) []Resource {
	rs := make([]Resource, n)
	for i := range rs {
		rs[i] = Resource{
			ID:       dev.ResourceID(KindDigitalIn, uint(i)),
			Physical: dev.PhysicalPin(uint(i)),
			Kind:     KindDigitalIn | KindTrigger,
			Name:     "L" + strconv.Itoa(i),
			MinHz:    min,
			MaxHz:    max,
			Rates:    slices.Clone(rates),
		}
	}
	return rs
}
