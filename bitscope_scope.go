package logic

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"go.bug.st/serial"
)

const (
	clockRate     = 40e6              // master clock, Hz
	clockPeriod   = 1 / clockRate     // seconds
	bufferSize    = 12 << 10          // capture buffer, bytes
	timeoutPeriod = 256 * clockPeriod // one Timeout register tick
	maxTimeout    = math.MaxUint16    // Timeout register limit
	maxPreTrigger = 5e6               // fastest rate with pre-trigger capture
	linkTimeout   = 2 * time.Second   // wait for echoes and dumps
	pollInterval  = 50 * time.Millisecond
)

// Channel is an analog input.
type Channel int

const (
	A Channel = iota
	B
)

func (c Channel) String() string { return string(rune('A' + c)) }

// These nominal x1 coefficients are from scopething (BS000501).
// They are not a calibration of the connected BS000500.
var analog = struct {
	la, lb, lc, ha, hb, hc float64
	scale, offset          float64
	// These are the nominal capture limits, not safety ratings.
	safeLow, safeHigh float64
}{1.1, -0.05, 0, 1.1, -0.05, -0.05, 18.333, -7.517, -5.5, 8}

// InputRange returns the nominal x1 capture range, in volts.
func InputRange() (low, high float64) { return analog.safeLow, analog.safeHigh }

func toConverter(v float64) float64 { return (v - analog.offset) / analog.scale }

// converterRange returns the ConverterLo and ConverterHi values that
// make the ADC span low to high volts.
func converterRange(low, high float64) (lo, hi float64) {
	l, h := toConverter(low), toConverter(high)
	return analog.la*l + analog.lb*h + analog.lc, analog.ha*h + analog.hb*l + analog.hc
}

// mode is a trace mode and its clock and buffer limits.
type mode struct {
	name       string
	trace      uint8
	buffer     uint8
	tickLow    int // fastest clock, in ticks
	tickHigh   int // slowest clock, in ticks
	divide     int // largest ClockScale
	channels   int
	sampleSize int // bytes per sample
}

// modes are the analog trace modes, best first.
var modes = []mode{
	{"Macro", 18, 4, 40, 16384, 1, 1, 2},
	{"MacroChop", 19, 5, 40, 16384, 1, 2, 2},
	{"Analog", 0, 0, 15, 40, 16383, 1, 1},
	{"AnalogChop", 2, 1, 13, 40, 16383, 2, 1},
	{"AnalogFast", 4, 0, 8, 14, 1, 1, 1},
	{"AnalogFastChop", 6, 1, 8, 40, 1, 2, 1},
	{"AnalogShot", 11, 0, 2, 5, 1, 1, 1},
	{"AnalogShotChop", 16, 1, 4, 5, 1, 2, 1},
}

// Trigger sets when a trace starts.
type Trigger struct {
	Source  Channel
	Level   float64 // volts
	Falling bool
	// Position is the trigger point as a fraction of the trace (0..1).
	Position float64
	// Timeout is how long to wait for a trigger before the device
	// captures anyway. Zero waits forever.
	Timeout time.Duration
}

// ScopeConfig sets up a BitScope analog capture.
type ScopeConfig struct {
	A, B      bool
	Duration  time.Duration // time span of the trace
	Samples   int           // wanted samples per channel; the device may give more or fewer
	Low, High float64       // input range, volts
	Trigger   Trigger
}

// ScopeTrace is the result of a BitScope analog capture.
type ScopeTrace struct {
	Mode       string
	Rate       float64 // samples per second, per channel
	Duration   time.Duration
	A, B       []float64 // volts; nil if the channel is off
	Triggered  bool      // false if the trace timed out
	TriggerAt  int       // sample index of the trigger point
	TriggerLvl float64
}

// plan is the register setup for one capture.
type plan struct {
	mode     mode
	ticks    int
	scale    int
	samples  int // total, all channels
	channels int
}

// rate returns the sample rate, all channels together.
func (p plan) rate() float64 { return clockRate / float64(p.ticks*p.scale) }

// choose finds the best trace mode for the given span and sample count.
func choose(channels int, span float64, samples int) (plan, error) {
	if channels < 1 || channels > 2 {
		return plan{}, errors.New("bitscope: enable one or two channels")
	}
	if span <= 0 || math.IsNaN(span) || math.IsInf(span, 0) || samples < 8 || samples > bufferSize {
		return plan{}, errors.New("bitscope: invalid capture duration or sample count")
	}
	total := samples * channels
	for _, m := range modes {
		if m.channels != channels {
			continue
		}
		perSample := span / clockPeriod / float64(total)
		ticks, scale := int(math.Round(perSample)), 1
		switch {
		case ticks > m.tickHigh && m.divide > 1:
			scale = min(m.divide, int(math.Ceil(perSample/float64(m.tickHigh))))
			ticks = int(math.Round(perSample / float64(scale)))
			if ticks <= m.tickLow {
				continue
			}
			ticks = min(ticks, m.tickHigh)
		case ticks >= m.tickLow:
			ticks = min(ticks, m.tickHigh)
		default:
			continue
		}
		n := int(math.Round(span / clockPeriod / float64(ticks*scale)))
		n -= n % channels
		if n <= bufferSize/m.sampleSize && n >= 32 {
			return plan{mode: m, ticks: ticks, scale: scale, samples: n, channels: channels}, nil
		}
	}
	return plan{}, fmt.Errorf("bitscope: no trace mode can capture %v with %d samples", time.Duration(span*1e9), samples)
}

// BitScope is an open BitScope.
type BitScope struct {
	vm   vm
	port serial.Port
	// ID is the device revision, for example "BS000500".
	ID string
}

// OpenBitScope opens the BitScope on the named serial port.
func OpenBitScope(name string) (*BitScope, error) {
	// The FT245R is a FIFO, so the baud rate has no effect.
	p, err := serial.Open(name, &serial.Mode{BaudRate: 460800})
	if err != nil {
		return nil, err
	}
	if err := p.SetReadTimeout(pollInterval); err != nil {
		p.Close()
		return nil, err
	}
	s, err := NewBitScope(p)
	if err != nil {
		p.Close()
		return nil, err
	}
	s.port = p
	return s, nil
}

// NewBitScope resets the device on p and reads its ID.
func NewBitScope(p Port) (*BitScope, error) {
	s := &BitScope{vm: vm{port: p, timeout: linkTimeout}}
	if err := s.vm.reset(); err != nil {
		return nil, err
	}
	id, err := s.vm.identify()
	if err != nil {
		return nil, err
	}
	if id != "BS000500" && id != "BS000501" {
		return nil, fmt.Errorf("bitscope: unsupported device %q", id)
	}
	s.ID = id
	return s, nil
}

// SetTrace enables raw command and reply logs on stdout. Call it before capture.
func (s *BitScope) SetTrace(enabled bool) {
	s.vm.trace = enabled
}

// Close closes the port. Do not call it during Capture.
func (s *BitScope) Close() error {
	if s.port != nil {
		return s.port.Close()
	}
	return nil
}

// Capture runs one trace and reads the samples. If ctx ends while the
// device waits for a trigger, Capture cancels the trace and returns
// ctx.Err().
func (s *BitScope) Capture(ctx context.Context, c ScopeConfig) (*ScopeTrace, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	channels := 0
	var enable uint8
	if c.A {
		channels++
		enable |= 1 << A
	}
	if c.B {
		channels++
		enable |= 1 << B
	}
	samples := c.Samples
	if samples == 0 {
		samples = 1024
	}
	// Short spans may be faster than the device can sample; use fewer
	// samples until a mode fits.
	var p plan
	var err error
	for ; samples >= 8; samples /= 2 {
		if p, err = choose(channels, c.Duration.Seconds(), samples); err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}

	t := c.Trigger
	pos := min(max(t.Position, 0), 1)
	if p.rate() > maxPreTrigger {
		pos = 0
	}
	const triggerOutro, triggerIntro = 8, 8
	trigAt := int(float64(p.samples) * pos)
	traceOutro := max(0, p.samples-trigAt-triggerOutro)
	traceIntro := max(0, trigAt-triggerIntro)

	timeout := 0
	if t.Timeout > 0 {
		capture := float64(triggerIntro+triggerOutro+traceOutro+2) / p.rate()
		timeout = int(math.Ceil((capture + t.Timeout.Seconds()) / timeoutPeriod))
		if timeout > maxTimeout {
			return nil, errors.New("bitscope: capture and trigger timeout exceed the 419 ms timer limit")
		}
	}

	spock := uint8(0x01) // hardware comparator
	logic := uint8(0x80)
	sinkA := uint8(0x80)
	if t.Source == B {
		spock |= 0x04
		logic = 0x40
		sinkA = 0x40
	}
	if t.Falling {
		spock |= 0x40
	}
	lo, hi := converterRange(c.Low, c.High)
	level := toConverter(t.Level)
	sinkB, err := s.vm.register(regKitchenSinkB)
	if err != nil {
		return nil, err
	}

	var r registers
	r.u8(regTraceMode, p.mode.trace)
	r.u8(regBufferMode, p.mode.buffer)
	r.u24(regSampleAddress, 0)
	r.u16(regClockTicks, uint16(p.ticks))
	r.u16(regClockScale, uint16(p.scale))
	r.frac16(regTriggerLevel, level)
	r.u8(regTriggerLogic, logic)
	r.u8(regTriggerMask, 0xff^logic)
	r.u16(regTraceIntro, uint16(traceIntro))
	r.u16(regTraceOutro, uint16(traceOutro))
	r.u32(regTraceDelay, 0)
	r.u16(regTimeout, uint16(timeout))
	r.u16(regTriggerIntro, triggerIntro/2)
	r.u16(regTriggerOutro, triggerOutro/2)
	r.u16(regPrelude, 0)
	r.u8(regSpockOption, spock)
	r.frac16(regConverterLo, lo)
	r.frac16(regConverterHi, hi)
	r.u8(regKitchenSinkA, sinkA)
	r.u8(regKitchenSinkB, sinkB|0x80) // keep the generator state
	r.u8(regAnalogEnable, enable)
	r.u8(regDigitalEnable, 0)
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
	addr -= addr % uint64(p.channels)

	tr := &ScopeTrace{
		Mode:       p.mode.name,
		Rate:       p.rate() / float64(p.channels),
		Duration:   time.Duration(float64(p.samples) / p.rate() * 1e9),
		Triggered:  status == statusDone,
		TriggerAt:  trigAt / p.channels,
		TriggerLvl: t.Level,
	}
	width := uint64(bufferSize / p.mode.sampleSize)
	start := (addr + width - uint64(p.samples)%width) % width
	per := p.samples / p.channels
	dumpChan := uint8(0)
	for _, ch := range []struct {
		on  bool
		dst *[]float64
	}{{c.A, &tr.A}, {c.B, &tr.B}} {
		if !ch.on {
			continue
		}
		v, err := s.dump(start, dumpChan, per, p.mode.sampleSize, c.Low, c.High)
		if err != nil {
			return nil, err
		}
		*ch.dst = v
		dumpChan++
	}
	return tr, nil
}

func (c ScopeConfig) validate() error {
	if !c.A && !c.B {
		return errors.New("bitscope: enable at least one channel")
	}
	if c.Duration <= 0 || c.Duration > time.Second {
		return errors.New("bitscope: capture duration must be more than zero and at most one second")
	}
	if c.Samples != 0 && (c.Samples < 8 || c.Samples > bufferSize) {
		return errors.New("bitscope: sample count must be zero or between 8 and 12288")
	}
	for _, f := range []float64{c.Low, c.High, c.Trigger.Level, c.Trigger.Position} {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return errors.New("bitscope: capture settings must be finite")
		}
	}
	if c.Low < analog.safeLow || c.High > analog.safeHigh || c.High <= c.Low {
		return errors.New("bitscope: invalid input range")
	}
	t := c.Trigger
	if t.Source != A && t.Source != B {
		return errors.New("bitscope: invalid trigger source")
	}
	if t.Source == A && !c.A || t.Source == B && !c.B {
		return errors.New("bitscope: trigger source must be enabled")
	}
	if t.Level < analog.safeLow || t.Level > analog.safeHigh ||
		t.Position < 0 || t.Position > 1 || t.Timeout < 0 || t.Timeout > time.Second {
		return errors.New("bitscope: invalid trigger settings")
	}
	return nil
}

// wait reads trace status replies until the trace ends.
func (s *BitScope) wait(ctx context.Context) (uint64, error) {
	for {
		if ctx.Err() != nil {
			if err := s.vm.cancelTrace(s.ID); err != nil {
				return 0, fmt.Errorf("bitscope: cancel trace: %w", err)
			}
			return 0, ctx.Err()
		}
		line, err := s.vm.reply(pollInterval)
		if errors.Is(err, errTimeout) {
			continue
		}
		if err != nil {
			return 0, err
		}
		status, err := strconv.ParseUint(line, 16, 8)
		if err != nil || status > statusStop {
			return 0, fmt.Errorf("bitscope: bad status %q", line)
		}
		if _, err := s.vm.hexReply(linkTimeout); err != nil { // timestamp
			return 0, err
		}
		if status == statusStop {
			return 0, errors.New("bitscope: trace stopped by device")
		}
		if status != statusWait {
			return status, nil
		}
	}
}

// dump reads n samples of one channel, starting at addr, as volts.
func (s *BitScope) dump(addr uint64, ch uint8, n, size int, low, high float64) ([]float64, error) {
	raw, err := s.dumpRaw(addr, ch, n, size)
	if err != nil {
		return nil, err
	}
	out := make([]float64, n)
	span := high - low
	for i := range out {
		var f float64
		if size == 2 {
			f = (float64(int16(binary.BigEndian.Uint16(raw[2*i:]))) + 32768) / 65536
		} else {
			f = float64(raw[i]) / 256
		}
		out[i] = low + f*span
	}
	return out, nil
}

func (s *BitScope) dumpRaw(addr uint64, ch uint8, n, size int) ([]byte, error) {
	var r registers
	r.u24(regSampleAddress, uint32(addr))
	dumpMode := uint8(0) // raw, 8 bit
	if size == 2 {
		dumpMode = 5 // native, 16 bit
	}
	r.u8(regDumpMode, dumpMode)
	r.u8(regDumpChan, ch)
	r.u16(regDumpCount, uint16(n))
	r.u16(regDumpRepeat, 1)
	r.u16(regDumpSend, 1)
	r.u16(regDumpSkip, 0)
	if err := s.vm.issue(r.String() + ">A"); err != nil {
		return nil, err
	}
	return s.vm.read(n*size, linkTimeout)
}
