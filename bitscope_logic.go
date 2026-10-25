package logic

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// String returns the device ID.
func (s *BitScope) String() string { return s.ID }

// ValidateLogic checks a logic configuration without accessing the device.
func (s *BitScope) ValidateLogic(c Config) error {
	_, _, _, err := bitscopeLogicPlan(c)
	return err
}

func bitscopeLogicPlan(c Config) (ticks, trigger, timeout int, err error) {
	switch {
	case c.Channels == 0 || c.Channels&^0x3f != 0:
		err = errors.New("bitscope: select inputs L0 through L5")
	case c.Source < 0 || c.Source > 5 || c.Channels&(1<<c.Source) == 0:
		err = errors.New("bitscope: enable the logic trigger input")
	case math.IsNaN(c.Rate) || math.IsInf(c.Rate, 0) || c.Rate < clockRate/16384 || c.Rate > 8e6:
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
	ticks = int(math.Ceil(clockRate / c.Rate))
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

// CaptureLogic captures all enabled digital inputs at the same time.
// Do not call it during another capture or generator operation.
func (s *BitScope) CaptureLogic(ctx context.Context, c Config) (*Trace, error) {
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
		Rate: clockRate / float64(ticks), Channels: c.Channels,
		Source: c.Source, Falling: c.Falling, TriggerAt: trigger,
		Triggered: status == statusDone, Samples: data,
	}, nil
}
