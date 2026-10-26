package logic

import (
	"fmt"
	"slices"
	"strconv"
	syncatomic "sync/atomic"
)

var lastDeviceID syncatomic.Uint64

type device struct {
	id    uint64
	class DeviceClass
}

func newDevice(class DeviceClass) *device {
	return &device{id: lastDeviceID.Add(1), class: class}
}

func (d *device) Class() DeviceClass { return d.class }

func (d *device) resourceID(kind ResourceKind, index uint) ResourceID {
	return ResourceID{device: d, kind: kind, index: index}
}

func (d *device) physicalPin(pin uint) PhysicalPin {
	return PhysicalPin{device: d, pin: pin}
}

// DeviceClass identifies a kind of analyser.
//
//go:generate go tool stringer -type=DeviceClass -linecomment
type DeviceClass uint64

const (
	ClassBitScope DeviceClass = iota + 1 // bitscope
	ClassDSLogic                         // dslogic
	ClassFX2                             // fx2lafw
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
	device *device
	kind   ResourceKind
	index  uint
}

// Device returns the analyser that owns the resource.
func (id ResourceID) Device() *device { return id.device }

// Kind returns the resource's role.
func (id ResourceID) Kind() ResourceKind { return id.kind }

// Index returns the resource's index on the analyser.
func (id ResourceID) Index() uint { return id.index }

// PhysicalPin identifies one pin or signal in a global namespace: device
// class, device instance, and pin. The same pin number on two classes, or
// on two analysers of one class, gives different IDs. Resources with the
// same PhysicalPin use the same hardware signal.
type PhysicalPin struct {
	device *device
	pin    uint
}

// Device returns the analyser that owns the pin.
func (p PhysicalPin) Device() *device { return p.device }

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

// CapabilitySet is the resources of one or more analysers and the
// relations between them.
type CapabilitySet struct {
	Resources []Resource
	Relations []Relation
}

// Resource returns the resource with the given ID.
func (s CapabilitySet) Resource(id ResourceID) (Resource, bool) {
	for _, r := range s.Resources {
		if r.ID == id {
			return r, true
		}
	}
	return Resource{}, false
}

// HasRelation reports whether a and b have a relation of the given kind,
// in either order.
func (s CapabilitySet) HasRelation(a, b ResourceID, kind RelationKind) bool {
	for _, r := range s.Relations {
		if r.Kind == kind && (r.A == a && r.B == b || r.A == b && r.B == a) {
			return true
		}
	}
	return false
}

// digitalInputs returns n digital inputs, L0 to Ln-1, on pins 0 to n-1,
// with the same rates. Each input can also be the trigger source.
func digitalInputs(dev *device, n int, min, max Frequency, rates []Frequency) []Resource {
	rs := make([]Resource, n)
	for i := range rs {
		rs[i] = Resource{
			ID:       dev.resourceID(KindDigitalIn, uint(i)),
			Physical: dev.physicalPin(uint(i)),
			Kind:     KindDigitalIn | KindTrigger,
			Name:     "L" + strconv.Itoa(i),
			MinHz:    min,
			MaxHz:    max,
			Rates:    slices.Clone(rates),
		}
	}
	return rs
}
