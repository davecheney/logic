// Package logic drives logic analysers from the command line. It supports
// the BitScope BS05, the original DreamSourceLab DSLogic, the Open Logic
// Sniffer, the Logic Pirate, and eight-channel 24 MHz Saleae Logic clones.
package logic

import (
	"context"
	"encoding/binary"
	"fmt"
	"slices"
	"time"
)

// CaptureRequest selects digital resources for one capture. The trigger is
// rising by default, falling with Falling, or either edge with AnyEdge.
type CaptureRequest struct {
	Inputs     []ResourceID
	Trigger    ResourceID
	Rate       Frequency
	Samples    int
	Falling    bool
	AnyEdge    bool // either edge; mutually exclusive with Falling
	PreTrigger float64
	Auto       time.Duration
}

// Waveform is the shape of an AWG output.
type Waveform string

const (
	Sine     Waveform = "Sine"
	Square   Waveform = "Square"
	Triangle Waveform = "Triangle"
	Saw      Waveform = "Saw"
)

// OutputRequest selects an output resource and its requested signal.
type OutputRequest struct {
	Resource  ResourceID
	Kind      ResourceKind
	Enable    bool
	Frequency Frequency
	Waveform  Waveform
}

// Trace holds packed samples. Bit n of a sample is the state of Inputs[n].
// A zero ResourceID in Inputs marks a bit that holds no input. The inputs
// can be on more than one device. Width is the number of bytes in each
// little-endian sample: zero or one, two, four, or eight. Trigger is the
// input that started the capture. Rate is the actual sample rate, rounded
// to the nearest milliHertz.
type Trace struct {
	Rate      Frequency
	Inputs    []ResourceID
	Trigger   ResourceID
	Falling   bool
	AnyEdge   bool
	TriggerAt int
	Triggered bool
	Width     int
	Samples   []byte
}

func (t *Trace) width() int { return max(t.Width, 1) }

// Len returns the number of samples.
func (t *Trace) Len() int { return len(t.Samples) / t.width() }

// At returns sample i.
func (t *Trace) At(i int) uint64 {
	switch t.width() {
	case 1:
		return uint64(t.Samples[i])
	case 2:
		return uint64(binary.LittleEndian.Uint16(t.Samples[2*i:]))
	case 4:
		return uint64(binary.LittleEndian.Uint32(t.Samples[4*i:]))
	default:
		return binary.LittleEndian.Uint64(t.Samples[8*i:])
	}
}

// Mask returns the sample bits that hold an input.
func (t *Trace) Mask() uint64 {
	var mask uint64
	for bit, id := range t.Inputs {
		if id != (ResourceID{}) {
			mask |= 1 << bit
		}
	}
	return mask
}

// Bit returns the sample bit that holds id.
func (t *Trace) Bit(id ResourceID) (int, bool) {
	if id == (ResourceID{}) {
		return 0, false
	}
	bit := slices.Index(t.Inputs, id)
	return bit, bit >= 0
}

// Analyzer is an open logic analyser. String returns the device ID.
// Capabilities describes its resources.
type Analyzer interface {
	fmt.Stringer
	Capabilities() CapabilitySet
	ValidateCapture(CaptureRequest) error
	CaptureLogic(context.Context, CaptureRequest) (*Trace, error)
	ConfigureAWG(OutputRequest) (Frequency, error)
	Close() error
}
