package logic

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// String returns the device name.
func (s *BitScope) String() string { return s.Name() }

// bitscopeLogicRates are suggested logic sample rates. Each is 40 MHz
// divided by a whole number of ticks.
var bitscopeLogicRates = []Frequency{
	8 * MegaHertz, 5 * MegaHertz, 4 * MegaHertz, 2 * MegaHertz, 1 * MegaHertz,
	500 * KiloHertz, 200 * KiloHertz, 100 * KiloHertz, 50 * KiloHertz,
	20 * KiloHertz, 10 * KiloHertz, 5 * KiloHertz, 2500 * Hertz,
}

// BitScope pins: L0 to L5 are pins 0 to 5. The AWG output uses the L4 pin.
const (
	bitscopePinA   = 6
	bitscopePinB   = 7
	bitscopePinAWG = 4
)

// Capabilities returns the resources of the BitScope. L4 shares its pin
// with the AWG output.
func (s *BitScope) Capabilities() CapabilitySet { return bitscopeCapabilities(s.device) }

func bitscopeCapabilities(dev *device) CapabilitySet {
	// The slowest logic rate is 40 MHz / 16384 ticks; round it up so that
	// validateLogic accepts it.
	clockRateMilliHz := Frequency(clockRate) * Hertz
	minLogic := (clockRateMilliHz + 16384 - 1) / 16384
	rs := digitalInputs(dev, 6, minLogic, 8*MegaHertz, bitscopeLogicRates)
	minAnalog, maxAnalog := scopeRateRange()
	for i, name := range []string{"A", "B"} {
		rs = append(rs, Resource{
			ID:       dev.resourceID(KindAnalogIn, uint(i)),
			Physical: dev.physicalPin(bitscopePinA + uint(i)),
			Kind:     KindAnalogIn | KindTrigger,
			Name:     name,
			MinHz:    minAnalog,
			MaxHz:    maxAnalog,
		})
	}
	awg := Resource{
		ID:        dev.resourceID(KindAWG, 0),
		Physical:  dev.physicalPin(bitscopePinAWG),
		Kind:      KindAWG,
		Name:      "awg0",
		MinHz:     10 * Hertz,
		MaxHz:     10 * KiloHertz,
		Waveforms: []Waveform{Sine, Square, Triangle, Saw},
	}
	rs = append(rs, awg)
	l4 := rs[4].ID
	sampleLimits := make([]SampleLimit, 6)
	for i := range sampleLimits {
		sampleLimits[i] = SampleLimit{InputCount: i + 1, MaxSamples: bufferSize}
	}
	return CapabilitySet{
		resources:    rs,
		sampleLimits: sampleLimits,
		relations: []Relation{
			{A: l4, B: awg.ID, Kind: RelationAlias},
			{A: l4, B: awg.ID, Kind: RelationConflict},
		},
	}
}

// scopeRateRange returns the slowest and fastest analog sample rates for
// one channel in the trace modes.
func scopeRateRange() (min, max Frequency) {
	clockRateMilliHz := Frequency(clockRate) * Hertz
	for _, m := range modes {
		if m.channels != 1 {
			continue
		}
		fast := clockRateMilliHz / Frequency(m.tickLow)
		slowDivisor := Frequency(m.tickHigh * m.divide)
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
func (s *BitScope) ValidateCapture(req CaptureRequest) error {
	c, err := captureConfig(s.device, s.Capabilities(), req)
	if err != nil {
		return err
	}
	return s.validateLogic(c)
}

func (s *BitScope) validateLogic(c logicConfig) error {
	_, _, _, err := bitscopeLogicPlan(c)
	return err
}

func bitscopeLogicPlan(c logicConfig) (ticks, trigger, timeout int, err error) {
	switch {
	case c.Channels == 0 || c.Channels&^0x3f != 0:
		err = errors.New("bitscope: select inputs L0 through L5")
	case c.Source < 0 || c.Source > 5 || c.Channels&(1<<c.Source) == 0:
		err = errors.New("bitscope: enable the logic trigger input")
	case c.Rate.Hz() < clockRate/16384 || c.Rate > 8*MegaHertz:
		err = errors.New("bitscope: logic sample rate must be between 2442 and 8000000 S/s")
	case c.Samples < 32 || c.Samples > bufferSize:
		err = errors.New("bitscope: logic sample count must be between 32 and 12288")
	case math.IsNaN(c.PreTrigger) || c.PreTrigger < 0 || c.PreTrigger > 1:
		err = errors.New("bitscope: pre-trigger fraction must be between 0 and 1")
	case c.Auto < 0:
		err = errors.New("bitscope: auto timeout must not be negative")
	}
	if err != nil {
		return
	}
	ticks = int(math.Ceil(clockRate / c.Rate.Hz()))
	trigger = int(float64(c.Samples) * c.PreTrigger)
	if c.PreTrigger > 0 && clockRate/float64(ticks) > maxPreTrigger {
		err = errors.New("bitscope: pre-trigger logic capture requires at most 5000000 S/s")
		return
	}
	if c.Auto > 0 {
		capture := float64(max(0, c.Samples-trigger-2)+6) * float64(ticks) / clockRate
		t := math.Ceil((capture + c.Auto.Seconds()) / timeoutPeriod)
		if t > maxTimeout {
			err = errors.New("bitscope: logic capture and auto timeout exceed the 419 ms timer limit")
			return
		}
		timeout = int(t)
	}
	return
}

// CaptureLogic captures the requested digital inputs at the same time.
// Do not call it during another capture or generator operation.
func (s *BitScope) CaptureLogic(ctx context.Context, req CaptureRequest) (*Trace, error) {
	c, err := captureConfig(s.device, s.Capabilities(), req)
	if err != nil {
		return nil, err
	}
	return s.captureLogic(ctx, c)
}

func (s *BitScope) captureLogic(ctx context.Context, c logicConfig) (*Trace, error) {
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
	return &Trace{
		Rate: frequencyFromHz(clockRate / float64(ticks)), Channels: c.Channels,
		Source: c.Source, Falling: c.Falling, TriggerAt: trigger,
		Triggered: status == statusDone, Samples: data,
	}, nil
}
