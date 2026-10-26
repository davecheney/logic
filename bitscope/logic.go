package bitscope

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
)

// String returns the device name.
func (s *BitScope) String() string { return s.Name() }

// bitscopeLogicRates are suggested logic sample rates. Each is 40 MHz
// divided by a whole number of ticks.
var bitscopeLogicRates = []logic.Frequency{
	8 * logic.MegaHertz, 5 * logic.MegaHertz, 4 * logic.MegaHertz, 2 * logic.MegaHertz, 1 * logic.MegaHertz,
	500 * logic.KiloHertz, 200 * logic.KiloHertz, 100 * logic.KiloHertz, 50 * logic.KiloHertz,
	20 * logic.KiloHertz, 10 * logic.KiloHertz, 5 * logic.KiloHertz, 2500 * logic.Hertz,
}

// BitScope pins: L0 to L5 are pins 0 to 5. The AWG output uses the L4 pin.
const (
	bitscopePinA   = 6
	bitscopePinB   = 7
	bitscopePinAWG = 4
)

// Capabilities returns the resources of the BitScope. L4 shares its pin
// with the AWG output.
func (s *BitScope) Capabilities() logic.CapabilitySet { return Capabilities(s.device) }

// Capabilities returns the resources of a BitScope device without accessing
// hardware.
func Capabilities(dev *logic.Device) logic.CapabilitySet {
	// The slowest logic rate is 40 MHz / 16384 ticks; round it up so that
	// validateLogic accepts it.
	clockRateMilliHz := logic.Frequency(clockRate) * logic.Hertz
	minLogic := (clockRateMilliHz + 16384 - 1) / 16384
	rs := logic.DigitalInputs(dev, 6, minLogic, 8*logic.MegaHertz, bitscopeLogicRates)
	minAnalog, maxAnalog := scopeRateRange()
	for i, name := range []string{"A", "B"} {
		rs = append(rs, logic.Resource{
			ID:       dev.ResourceID(logic.KindAnalogIn, uint(i)),
			Physical: dev.PhysicalPin(bitscopePinA + uint(i)),
			Kind:     logic.KindAnalogIn | logic.KindTrigger,
			Name:     name,
			MinHz:    minAnalog,
			MaxHz:    maxAnalog,
		})
	}
	awg := logic.Resource{
		ID:        dev.ResourceID(logic.KindAWG, 0),
		Physical:  dev.PhysicalPin(bitscopePinAWG),
		Kind:      logic.KindAWG,
		Name:      "awg0",
		MinHz:     10 * logic.Hertz,
		MaxHz:     10 * logic.KiloHertz,
		Waveforms: []logic.Waveform{logic.Sine, logic.Square, logic.Triangle, logic.Saw},
	}
	rs = append(rs, awg)
	l4 := rs[4].ID
	sampleLimits := make([]logic.SampleLimit, 6)
	for i := range sampleLimits {
		sampleLimits[i] = logic.SampleLimit{InputCount: i + 1, MaxSamples: bufferSize}
	}
	return logic.NewCapabilitySet(rs, []logic.Relation{
		{A: l4, B: awg.ID, Kind: logic.RelationAlias},
		{A: l4, B: awg.ID, Kind: logic.RelationConflict},
	}, sampleLimits)
}

// scopeRateRange returns the slowest and fastest analog sample rates for
// one channel in the trace modes.
func scopeRateRange() (logic.Frequency, logic.Frequency) {
	var min, max logic.Frequency
	clockRateMilliHz := logic.Frequency(clockRate) * logic.Hertz
	for _, m := range modes {
		if m.channels != 1 {
			continue
		}
		fast := clockRateMilliHz / logic.Frequency(m.tickLow)
		slowDivisor := logic.Frequency(m.tickHigh * m.divide)
		slow := (clockRateMilliHz + slowDivisor - 1) / slowDivisor
		if max == 0 || fast > max {
			max = fast
		}
		if min == 0 || slow < min {
			min = slow
		}
	}
	return min, max
}

// ValidateCapture checks a typed capture request without accessing the device.
func (s *BitScope) ValidateCapture(req logic.CaptureRequest) error { return Validate(s.device, req) }

// Validate checks req against the resources of dev, which must be of the
// BitScope class, without accessing hardware.
func Validate(dev *logic.Device, req logic.CaptureRequest) error {
	c, err := capture.FromRequest(dev, Capabilities(dev), req)
	if err != nil {
		return err
	}
	return (&BitScope{}).validateLogic(c)
}

func (s *BitScope) validateLogic(c capture.Config) error {
	_, _, _, err := bitscopeLogicPlan(c)
	return err
}

func bitscopeLogicPlan(c capture.Config) (int, int, int, error) {
	var ticks, trigger, timeout int
	var err error
	switch {
	case c.AnyEdge:
		err = errors.New("bitscope: any-edge triggers are not supported")
	case c.Channels == 0 || c.Channels&^0x3f != 0:
		err = errors.New("bitscope: select inputs L0 through L5")
	case c.Source < 0 || c.Source > 5 || c.Channels&(1<<c.Source) == 0:
		err = errors.New("bitscope: enable the logic trigger input")
	case c.Rate.Hz() < clockRate/16384 || c.Rate > 8*logic.MegaHertz:
		err = errors.New("bitscope: logic sample rate must be between 2442 and 8000000 S/s")
	case c.Samples < 32 || c.Samples > bufferSize:
		err = errors.New("bitscope: logic sample count must be between 32 and 12288")
	case math.IsNaN(c.PreTrigger) || c.PreTrigger < 0 || c.PreTrigger > 1:
		err = errors.New("bitscope: pre-trigger fraction must be between 0 and 1")
	case c.Auto < 0:
		err = errors.New("bitscope: auto timeout must not be negative")
	}
	if err != nil {
		return ticks, trigger, timeout, err
	}
	ticks = int(math.Ceil(clockRate / c.Rate.Hz()))
	trigger = int(float64(c.Samples) * c.PreTrigger)
	if c.PreTrigger > 0 && clockRate/float64(ticks) > maxPreTrigger {
		err = errors.New("bitscope: pre-trigger logic capture requires at most 5000000 S/s")
		return ticks, trigger, timeout, err
	}
	if c.Auto > 0 {
		capture := float64(max(0, c.Samples-trigger-2)+6) * float64(ticks) / clockRate
		t := math.Ceil((capture + c.Auto.Seconds()) / timeoutPeriod)
		if t > maxTimeout {
			err = errors.New("bitscope: logic capture and auto timeout exceed the 419 ms timer limit")
			return ticks, trigger, timeout, err
		}
		timeout = int(t)
	}
	return ticks, trigger, timeout, nil
}

// CaptureLogic captures the requested digital inputs at the same time.
// Do not call it during another capture or generator operation.
func (s *BitScope) CaptureLogic(ctx context.Context, req logic.CaptureRequest) (*logic.Trace, error) {
	c, err := capture.FromRequest(s.device, s.Capabilities(), req)
	if err != nil {
		return nil, err
	}
	return s.captureLogic(ctx, c)
}

func (s *BitScope) captureLogic(ctx context.Context, c capture.Config) (*logic.Trace, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ticks, trigger, timeout, err := bitscopeLogicPlan(c)
	if err != nil {
		return nil, err
	}
	sinkB, err := s.vm.register(regKitchenSinkB)
	if err != nil {
		return nil, err
	}
	if c.Channels&(1<<4) != 0 && sinkB&0x40 != 0 {
		return nil, errors.New("bitscope: L4 is in use by the AWG; stop the AWG before logic capture")
	}
	var r registers
	r.u8(regTraceMode, 14) // tmLogic: 5..16384 clock ticks, one byte per sample
	r.u8(regBufferMode, 0)
	r.u24(regSampleAddress, 0)
	r.u16(regClockTicks, uint16(ticks))
	r.u16(regClockScale, 1)
	r.u8(regTriggerLogic, 1<<c.Source)
	r.u8(regTriggerMask, ^uint8(1<<c.Source))
	r.u16(regTraceIntro, uint16(max(0, trigger-2)))
	r.u16(regTraceOutro, uint16(max(0, c.Samples-trigger-2)))
	r.u32(regTraceDelay, 0)
	r.u16(regTimeout, uint16(timeout))
	r.u16(regTriggerIntro, 1)
	r.u16(regTriggerOutro, 1)
	r.u16(regPrelude, 0)
	spock := uint8(1)
	if c.Falling {
		spock |= 0x40
	}
	r.u8(regSpockOption, spock)
	r.u8(regKitchenSinkA, 0)
	r.u8(regKitchenSinkB, sinkB)
	r.u8(regAnalogEnable, 0)
	r.u8(regDigitalEnable, uint8(c.Channels))
	if err := s.vm.issue(r.String() + ">UD"); err != nil {
		return nil, err
	}
	status, err := s.wait(ctx)
	if err != nil {
		return nil, err
	}
	addr, err := s.vm.hexReply(linkTimeout)
	if err != nil {
		return nil, err
	}
	start := (addr + bufferSize - uint64(c.Samples)%bufferSize) % bufferSize
	data, err := s.dumpRaw(start, 0x80, c.Samples, 1)
	if err != nil {
		return nil, fmt.Errorf("bitscope: logic dump: %w", err)
	}
	for i := range data {
		data[i] &= uint8(c.Channels)
	}
	return &logic.Trace{
		Rate:    logic.FrequencyFromHz(clockRate / float64(ticks)),
		Inputs:  s.device.TraceInputs(uint64(c.Channels)),
		Trigger: s.device.ResourceID(logic.KindDigitalIn, uint(c.Source)),
		Falling: c.Falling, TriggerAt: trigger,
		Triggered: status == statusDone, Samples: data,
	}, nil
}
