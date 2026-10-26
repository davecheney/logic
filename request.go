package logic

import "fmt"

func captureConfig(dev *device, caps CapabilitySet, req CaptureRequest) (logicConfig, error) {
	if len(req.Inputs) == 0 {
		return logicConfig{}, fmt.Errorf("logic: capture needs at least one input")
	}
	var channels uint16
	selected := make(map[ResourceID]bool, len(req.Inputs))
	for _, id := range req.Inputs {
		if id.Device() != dev {
			return logicConfig{}, fmt.Errorf("logic: capture input %+v is not a digital input on this analyser", id)
		}
		if id.Index() >= 16 {
			return logicConfig{}, fmt.Errorf("logic: capture input index %d is out of range", id.Index())
		}
		resource := caps.Resource(id)
		if resource == nil || !resource.Kind.Has(KindDigitalIn) {
			return logicConfig{}, fmt.Errorf("logic: capture input %+v is not available", id)
		}
		mask := uint16(1 << id.Index())
		if channels&mask != 0 {
			return logicConfig{}, fmt.Errorf("logic: capture input %+v is selected more than once", id)
		}
		channels |= mask
		selected[id] = true
	}

	trigger := caps.Resource(req.Trigger)
	if req.Trigger.Device() != dev || trigger == nil || !trigger.Kind.Has(KindTrigger) {
		return logicConfig{}, fmt.Errorf("logic: trigger %+v is not available on this analyser", req.Trigger)
	}
	if !selected[req.Trigger] {
		return logicConfig{}, fmt.Errorf("logic: trigger %+v is not selected as an input", req.Trigger)
	}

	return logicConfig{
		Channels: channels, Rate: req.Rate, Samples: req.Samples,
		Source: int(req.Trigger.Index()), Falling: req.Falling,
		PreTrigger: req.PreTrigger, Auto: req.Auto,
	}, nil
}

func validateOutputRequest(dev *device, caps CapabilitySet, req OutputRequest) error {
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
	if req.Kind != KindAWG {
		return fmt.Errorf("logic: output role %b is not supported", req.Kind)
	}
	return nil
}
