// Package logic drives logic analysers from the command line. It supports
// the BitScope BS05, the original DreamSourceLab DSLogic, and eight-channel
// 24 MHz Saleae Logic clones.
package logic

import (
	"context"
	"fmt"
	"time"
)

// logicConfig is the driver-specific view of a digital capture request.
type logicConfig struct {
	Channels   uint16
	Rate       Frequency // requested sample rate; rounded to a device clock step
	Samples    int
	Source     int // trigger input
	Falling    bool
	PreTrigger float64       // fraction of samples before the trigger
	Auto       time.Duration // zero waits for a trigger; otherwise permits an auto trace
}

// CaptureRequest selects digital resources for one capture.
type CaptureRequest struct {
	Inputs     []ResourceID
	Trigger    ResourceID
	Rate       Frequency
	Samples    int
	Falling    bool
	PreTrigger float64
	Auto       time.Duration
}

// OutputRequest selects an output resource and its requested signal.
type OutputRequest struct {
	Resource  ResourceID
	Kind      ResourceKind
	Enable    bool
	Frequency Frequency
	Waveform  Waveform
}

// Trace holds packed samples. Bit n of a sample is the state of Ln.
// Width is the number of bytes in each sample: zero or one for L0 through
// L7, or two (little endian) for L0 through L15. JSON encodes Samples as
// base64. Rate is the actual sample rate, rounded to the nearest milliHertz.
type Trace struct {
	Rate      Frequency `json:"sample_rate"`
	Channels  uint16    `json:"channels"`
	Source    int       `json:"trigger_channel"`
	Falling   bool      `json:"falling"`
	TriggerAt int       `json:"trigger_sample"`
	Triggered bool      `json:"triggered"`
	Width     int       `json:"sample_bytes,omitempty"`
	Samples   []byte    `json:"samples"`
}

// Len returns the number of samples.
func (t *Trace) Len() int {
	if t.Width == 2 {
		return len(t.Samples) / 2
	}
	return len(t.Samples)
}

// At returns sample i.
func (t *Trace) At(i int) uint16 {
	if t.Width == 2 {
		return uint16(t.Samples[2*i]) | uint16(t.Samples[2*i+1])<<8
	}
	return uint16(t.Samples[i])
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
