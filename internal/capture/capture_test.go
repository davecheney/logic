package capture_test

import (
	"slices"
	"testing"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/bitscope"
	"github.com/davecheney/logic/internal/capture"
)

func TestFromRequest(t *testing.T) {
	dev := logic.NewDevice(logic.ClassBitScope)
	caps := bitscope.Capabilities(dev)
	rs := slices.Collect(caps.Resources())
	req := logic.CaptureRequest{
		Inputs:  []logic.ResourceID{rs[0].ID, rs[1].ID},
		Trigger: rs[1].ID,
		Rate:    4 * logic.MegaHertz, Samples: 1200, Falling: true,
		PreTrigger: 0.25, Auto: time.Second,
	}
	got, err := capture.FromRequest(dev, caps, req)
	if err != nil {
		t.Fatal(err)
	}
	want := capture.Config{
		Channels: 3, Rate: req.Rate, Samples: req.Samples, Source: 1,
		Falling: true, PreTrigger: 0.25, Auto: time.Second,
	}
	if got != want {
		t.Fatalf("capture config: got %+v, want %+v", got, want)
	}
}

func TestFromRequestUsesResourceRoles(t *testing.T) {
	dev := logic.NewDevice(logic.ClassBitScope)
	id := dev.ResourceID(logic.KindAnalogIn, 0)
	caps := logic.NewCapabilitySet([]logic.Resource{{
		ID: id, Kind: logic.KindAnalogIn | logic.KindDigitalIn | logic.KindTrigger,
	}}, nil, nil)
	req := logic.CaptureRequest{
		Inputs: []logic.ResourceID{id}, Trigger: id, Rate: 4 * logic.MegaHertz, Samples: 1200,
	}
	got, err := capture.FromRequest(dev, caps, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Channels != 1 || got.Source != 0 {
		t.Fatalf("capture config: %+v", got)
	}
}

func TestFromRequestRejectsInvalidResources(t *testing.T) {
	dev := logic.NewDevice(logic.ClassBitScope)
	caps := bitscope.Capabilities(dev)
	rs := slices.Collect(caps.Resources())
	input0, input1 := rs[0].ID, rs[1].ID
	base := logic.CaptureRequest{Inputs: []logic.ResourceID{input0, input1}, Trigger: input1, Rate: 4 * logic.MegaHertz, Samples: 1200}
	other := bitscope.Capabilities(logic.NewDevice(logic.ClassBitScope))
	otherInput := slices.Collect(other.Resources())[0].ID
	unknown := dev.ResourceID(logic.KindDigitalIn, 15)
	analog := rs[6].ID

	tests := []struct {
		name string
		edit func(*logic.CaptureRequest)
	}{
		{"no inputs", func(r *logic.CaptureRequest) { r.Inputs = nil }},
		{"unknown input", func(r *logic.CaptureRequest) { r.Inputs = []logic.ResourceID{unknown} }},
		{"other device", func(r *logic.CaptureRequest) { r.Inputs = []logic.ResourceID{otherInput} }},
		{"wrong role", func(r *logic.CaptureRequest) { r.Inputs = []logic.ResourceID{analog} }},
		{"duplicate input", func(r *logic.CaptureRequest) { r.Inputs = []logic.ResourceID{input0, input0} }},
		{"trigger not selected", func(r *logic.CaptureRequest) { r.Trigger = rs[2].ID }},
		{"invalid trigger", func(r *logic.CaptureRequest) { r.Trigger = analog }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := base
			req.Inputs = slices.Clone(base.Inputs)
			tt.edit(&req)
			if _, err := capture.FromRequest(dev, caps, req); err == nil {
				t.Fatal("invalid capture request accepted")
			}
		})
	}
}

func TestValidateOutput(t *testing.T) {
	dev := logic.NewDevice(logic.ClassBitScope)
	caps := bitscope.Capabilities(dev)
	rs := slices.Collect(caps.Resources())
	awg := rs[len(rs)-1].ID
	if err := capture.ValidateOutput(dev, caps, logic.OutputRequest{Resource: awg, Kind: logic.KindAWG}); err != nil {
		t.Fatal(err)
	}
	if err := capture.ValidateOutput(dev, caps, logic.OutputRequest{Resource: rs[0].ID, Kind: logic.KindAWG}); err == nil {
		t.Fatal("input accepted as an AWG")
	}
	if err := capture.ValidateOutput(dev, caps, logic.OutputRequest{Resource: awg, Kind: logic.KindAWG | logic.KindClockOut}); err == nil {
		t.Fatal("multiple output roles accepted")
	}
}
