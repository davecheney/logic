// Package capture converts typed requests into the settings that the
// drivers use.
package capture

import (
	"fmt"
	"time"

	"github.com/davecheney/logic"
)

// Config is the driver view of a digital capture request.
type Config struct {
	Channels   uint16
	Rate       logic.Frequency // requested sample rate; rounded to a device clock step
	Samples    int
	Source     int // trigger input
	Falling    bool
	AnyEdge    bool
	PreTrigger float64       // fraction of samples before the trigger
	Auto       time.Duration // zero waits for a trigger; otherwise permits an auto trace
}

// FromRequest checks req against the resources of dev and returns its
// driver settings.
func FromRequest(dev *logic.Device, caps logic.CapabilitySet, req logic.CaptureRequest) (Config, error) {
	if req.AnyEdge && req.Falling {
		return Config{}, fmt.Errorf("logic: any-edge and falling-edge triggers are mutually exclusive")
	}
	if len(req.Inputs) == 0 {
		return Config{}, fmt.Errorf("logic: capture needs at least one input")
	}
	var channels uint16
	selected := make(map[logic.ResourceID]bool, len(req.Inputs))
	for _, id := range req.Inputs {
		if id.Device() != dev {
			return Config{}, fmt.Errorf("logic: capture input %+v is not a digital input on this analyser", id)
		}
		if id.Index() >= 16 {
			return Config{}, fmt.Errorf("logic: capture input index %d is out of range", id.Index())
		}
		resource := caps.Resource(id)
		if resource == nil || !resource.Kind.Has(logic.KindDigitalIn) {
			return Config{}, fmt.Errorf("logic: capture input %+v is not available", id)
		}
		mask := uint16(1 << id.Index())
		if channels&mask != 0 {
			return Config{}, fmt.Errorf("logic: capture input %+v is selected more than once", id)
		}
		channels |= mask
		selected[id] = true
	}

	trigger := caps.Resource(req.Trigger)
	if req.Trigger.Device() != dev || trigger == nil || !trigger.Kind.Has(logic.KindTrigger) {
		return Config{}, fmt.Errorf("logic: trigger %+v is not available on this analyser", req.Trigger)
	}
	if !selected[req.Trigger] {
		return Config{}, fmt.Errorf("logic: trigger %+v is not selected as an input", req.Trigger)
	}

	return Config{
		Channels: channels, Rate: req.Rate, Samples: req.Samples,
		Source: int(req.Trigger.Index()), Falling: req.Falling, AnyEdge: req.AnyEdge,
		PreTrigger: req.PreTrigger, Auto: req.Auto,
	}, nil
}

// ValidateOutput checks that req selects a supported output role on dev.
func ValidateOutput(dev *logic.Device, caps logic.CapabilitySet, req logic.OutputRequest) error {
	if req.Resource.Device() != dev {
		return fmt.Errorf("logic: output resource %+v is not on this analyser", req.Resource)
	}
	resource := caps.Resource(req.Resource)
	if resource == nil {
		return fmt.Errorf("logic: output resource %+v is not available", req.Resource)
	}
	if req.Kind == 0 || req.Kind&(req.Kind-1) != 0 || !resource.Kind.Has(req.Kind) {
		return fmt.Errorf("logic: output resource %+v does not support role %b", req.Resource, req.Kind)
	}
	if req.Kind != logic.KindAWG {
		return fmt.Errorf("logic: output role %b is not supported", req.Kind)
	}
	return nil
}
