package logic

import (
	"testing"
	"time"
)

func captureRequestForTest(dev Analyzer, c logicConfig) CaptureRequest {
	req := CaptureRequest{
		Rate: c.Rate, Samples: c.Samples, Falling: c.Falling,
		PreTrigger: c.PreTrigger, Auto: c.Auto,
	}
	caps := dev.Capabilities()
	for r := range caps.Resources() {
		if !r.Kind.Has(KindDigitalIn) {
			continue
		}
		index := r.ID.Index()
		if c.Channels&(1<<index) != 0 {
			req.Inputs = append(req.Inputs, r.ID)
		}
		if int(index) == c.Source {
			req.Trigger = r.ID
		}
	}
	return req
}

func TestCaptureConfig(t *testing.T) {
	dev := newDevice(ClassBitScope)
	caps := bitscopeCapabilities(dev)
	req := CaptureRequest{
		Inputs:  []ResourceID{caps.resources[0].ID, caps.resources[1].ID},
		Trigger: caps.resources[1].ID,
		Rate:    4 * MegaHertz, Samples: 1200, Falling: true,
		PreTrigger: 0.25, Auto: time.Second,
	}
	got, err := captureConfig(dev, caps, req)
	if err != nil {
		t.Fatal(err)
	}
	want := logicConfig{
		Channels: 3, Rate: req.Rate, Samples: req.Samples, Source: 1,
		Falling: true, PreTrigger: 0.25, Auto: time.Second,
	}
	if got != want {
		t.Fatalf("capture config: got %+v, want %+v", got, want)
	}
}

func TestCaptureConfigUsesResourceRoles(t *testing.T) {
	dev := newDevice(ClassBitScope)
	id := dev.resourceID(KindAnalogIn, 0)
	caps := CapabilitySet{resources: []Resource{{
		ID: id, Kind: KindAnalogIn | KindDigitalIn | KindTrigger,
	}}}
	req := CaptureRequest{
		Inputs: []ResourceID{id}, Trigger: id, Rate: 4 * MegaHertz, Samples: 1200,
	}
	got, err := captureConfig(dev, caps, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Channels != 1 || got.Source != 0 {
		t.Fatalf("capture config: %+v", got)
	}
}

func TestCaptureConfigRejectsInvalidResources(t *testing.T) {
	dev := newDevice(ClassBitScope)
	caps := bitscopeCapabilities(dev)
	input0, input1 := caps.resources[0].ID, caps.resources[1].ID
	base := CaptureRequest{Inputs: []ResourceID{input0, input1}, Trigger: input1, Rate: 4 * MegaHertz, Samples: 1200}
	other := newDevice(ClassBitScope)
	otherInput := bitscopeCapabilities(other).resources[0].ID
	unknown := dev.resourceID(KindDigitalIn, 15)
	analog := caps.resources[6].ID

	tests := []struct {
		name string
		edit func(*CaptureRequest)
	}{
		{"no inputs", func(r *CaptureRequest) { r.Inputs = nil }},
		{"unknown input", func(r *CaptureRequest) { r.Inputs = []ResourceID{unknown} }},
		{"other device", func(r *CaptureRequest) { r.Inputs = []ResourceID{otherInput} }},
		{"wrong role", func(r *CaptureRequest) { r.Inputs = []ResourceID{analog} }},
		{"duplicate input", func(r *CaptureRequest) { r.Inputs = []ResourceID{input0, input0} }},
		{"trigger not selected", func(r *CaptureRequest) { r.Trigger = caps.resources[2].ID }},
		{"invalid trigger", func(r *CaptureRequest) { r.Trigger = analog }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := base
			req.Inputs = append([]ResourceID(nil), base.Inputs...)
			tt.edit(&req)
			if _, err := captureConfig(dev, caps, req); err == nil {
				t.Fatal("invalid capture request accepted")
			}
		})
	}
}

func TestValidateCaptureRequest(t *testing.T) {
	dev := &FX2{device: newDevice(ClassFX2)}
	req := captureRequestForTest(dev, logicConfig{
		Channels: 1, Rate: MegaHertz, Samples: 4096, Source: 0,
	})
	if err := Validate("fx2lafw", req); err != nil {
		t.Fatal(err)
	}
	if err := dev.ValidateCapture(req); err != nil {
		t.Fatal(err)
	}
	if err := Validate("dslogic", req); err == nil {
		t.Fatal("request for the wrong device class accepted")
	}
}

func TestOutputRequestValidation(t *testing.T) {
	dev := newDevice(ClassBitScope)
	caps := bitscopeCapabilities(dev)
	awg := caps.resources[len(caps.resources)-1].ID
	if err := validateOutputRequest(dev, caps, OutputRequest{Resource: awg, Kind: KindAWG}); err != nil {
		t.Fatal(err)
	}
	input := caps.resources[0].ID
	if err := validateOutputRequest(dev, caps, OutputRequest{Resource: input, Kind: KindAWG}); err == nil {
		t.Fatal("input accepted as an AWG")
	}
	if err := validateOutputRequest(dev, caps, OutputRequest{Resource: awg, Kind: KindAWG | KindClockOut}); err == nil {
		t.Fatal("multiple output roles accepted")
	}
	fx2 := &FX2{device: newDevice(ClassFX2)}
	if _, err := fx2.ConfigureAWG(OutputRequest{
		Resource: fx2.Capabilities().resources[0].ID,
		Kind:     KindDigitalOut,
		Enable:   true,
	}); err == nil {
		t.Fatal("unsupported output request accepted")
	}
}
