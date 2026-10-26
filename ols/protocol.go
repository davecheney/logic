package ols

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
)

// This file encodes the SUMP protocol of the Open Workbench Logic Sniffer
// with Demon Core FPGA firmware. It supports L0 through L15 (the buffered
// inputs) on the 100 MHz internal clock. It does no serial I/O.

const (
	olsCmdReset    = 0x00
	olsCmdArm      = 0x01
	olsCmdID       = 0x02
	olsCmdMetadata = 0x04
	olsCmdFinish   = 0x05

	olsCmdDivider      = 0x80
	olsCmdCaptureSize  = 0x81
	olsCmdFlags        = 0x82
	olsCmdTriggerMask  = 0xc0 // stage n is 0xc0 + 4n
	olsCmdTriggerValue = 0xc1
	olsCmdTriggerConf  = 0xc2

	olsTriggerStart = 1 << 27 // in the trigger configuration word

	olsFlagGroup0Off = 1 << 2 // groups 1 to 3 follow
	olsAllGroupsOff  = 0xf << 2
	olsClock         = 100 * logic.MegaHertz
	olsMaxDivider    = 1 << 24
	olsMinSamples    = 4
	olsMemoryBytes   = 24576
)

// olsID is the reply to olsCmdID.
var olsID = []byte("1ALS")

// olsMetadata is the reply to olsCmdMetadata.
type olsMetadata struct {
	Name       string
	FPGA       string
	Ancillary  string
	Probes     int
	Memory     int // sample memory in bytes
	MaxRate    logic.Frequency
	Protocol   int
	DynamicMem int
}

// parseOLSMetadata decodes a metadata reply. A key's top three bits give
// the type: 0 is a NUL-terminated string, 1 is a big endian uint32, and 2
// is one byte. Key 0 ends the reply.
func parseOLSMetadata(b []byte) (*olsMetadata, error) {
	m := new(olsMetadata)
	for i := 0; ; {
		if i >= len(b) {
			return nil, errors.New("ols: metadata has no end marker")
		}
		key := b[i]
		i++
		if key == 0 {
			return m, nil
		}
		switch key >> 5 {
		case 0:
			n := 0
			for i+n < len(b) && b[i+n] != 0 {
				n++
			}
			if i+n >= len(b) {
				return nil, fmt.Errorf("ols: metadata string %#02x is not terminated", key)
			}
			s := string(b[i : i+n])
			i += n + 1
			switch key {
			case 0x01:
				m.Name = s
			case 0x02:
				m.FPGA = s
			case 0x03:
				m.Ancillary = s
			}
		case 1:
			if i+4 > len(b) {
				return nil, fmt.Errorf("ols: metadata value %#02x is short", key)
			}
			v := binary.BigEndian.Uint32(b[i:])
			i += 4
			switch key {
			case 0x20:
				m.Probes = int(v)
			case 0x21:
				m.Memory = int(v)
			case 0x22:
				m.DynamicMem = int(v)
			case 0x23:
				m.MaxRate = logic.Frequency(v) * logic.Hertz
			case 0x24:
				m.Protocol = int(v)
			}
		case 2:
			if i >= len(b) {
				return nil, fmt.Errorf("ols: metadata value %#02x is short", key)
			}
			v := int(b[i])
			i++
			switch key {
			case 0x40:
				m.Probes = v
			case 0x41:
				m.Protocol = v
			}
		default:
			return nil, fmt.Errorf("ols: metadata key %#02x has unknown type", key)
		}
	}
}

// olsLong encodes a long command: the command byte, then a little endian
// uint32.
func olsLong(cmd byte, v uint32) []byte {
	return binary.LittleEndian.AppendUint32([]byte{cmd}, v)
}

// olsCapture holds the commands and decoding parameters for one capture.
type olsCapture struct {
	divider   uint32 // clock ticks per sample, minus one
	rate      logic.Frequency
	flags     uint32
	groups    int // enabled eight-channel groups, sent in each sample
	readCount int // samples sent by the device; a multiple of four
	delay     int // samples after the trigger; a multiple of four
	samples   int // samples returned in the trace
	triggerAt int
	mask      [2]uint32 // trigger stages 0 and 1
	value     [2]uint32
}

// olsPlan checks c and builds the capture for a device with memory bytes
// of sample memory. The rate rounds down to the next 100 MHz clock step.
func olsPlan(c capture.Config, memory int) (*olsCapture, error) {
	var groups int
	for g := range 2 {
		if c.Channels&(0xff<<(8*g)) != 0 {
			groups++
		}
	}
	switch {
	case c.Channels == 0:
		return nil, errors.New("ols: select at least one input")
	case c.Rate == 0 || c.Rate > olsClock:
		return nil, fmt.Errorf("ols: sample rate must be between %v and %v", olsMinRate(), olsClock)
	case c.Source < 0 || c.Source > 15 || c.Channels&(1<<c.Source) == 0:
		return nil, errors.New("ols: enable the logic trigger input")
	case memory < olsMinSamples:
		return nil, errors.New("ols: device reports no sample memory")
	case c.Samples < olsMinSamples || c.Samples > memory/groups&^3:
		return nil, fmt.Errorf("ols: sample count must be between %d and %d for %d inputs",
			olsMinSamples, memory/groups&^3, bits.OnesCount16(c.Channels))
	case math.IsNaN(c.PreTrigger) || c.PreTrigger < 0 || c.PreTrigger > 1:
		return nil, errors.New("ols: pre-trigger fraction must be between 0 and 1")
	case c.Auto < 0:
		return nil, errors.New("ols: auto timeout must not be negative")
	}
	ticks := (olsClock + c.Rate - 1) / c.Rate
	if ticks > olsMaxDivider {
		return nil, fmt.Errorf("ols: sample rate must be between %v and %v", olsMinRate(), olsClock)
	}
	p := &olsCapture{
		divider: uint32(ticks - 1),
		rate:    logic.FrequencyFromHz(olsClock.Hz() / float64(ticks)),
		flags:   olsAllGroupsOff,
		groups:  groups,
		samples: c.Samples,
	}
	for g := range 2 {
		if c.Channels&(0xff<<(8*g)) != 0 {
			p.flags &^= olsFlagGroup0Off << g
		}
	}
	// Counts are in units of four samples.
	units := (c.Samples + 3) / 4
	delay := units - int(c.PreTrigger*float64(units))
	delay = max(delay, 1)
	p.readCount, p.delay = 4*units, 4*delay

	// Stage 0 matches the level before the edge; stage 1 starts the
	// capture on the level after it.
	bit := uint32(1) << c.Source
	p.mask = [2]uint32{bit, bit}
	if c.Falling {
		p.value = [2]uint32{bit, 0}
	} else {
		p.value = [2]uint32{0, bit}
	}
	p.triggerAt = max(0, p.readCount-p.delay-len(p.mask))
	return p, nil
}

// olsMinRate returns the lowest internal-clock sample rate.
func olsMinRate() logic.Frequency {
	return (olsClock + olsMaxDivider - 1) / olsMaxDivider
}

// commands returns the bytes that configure and arm the capture. They
// start with a reset, which the Demon Core needs before each arm.
func (p *olsCapture) commands() []byte {
	b := make([]byte, 5, 64)
	for i := range b {
		b[i] = olsCmdReset
	}
	for stage := range p.mask {
		conf := uint32(stage) << 16
		if stage == len(p.mask)-1 {
			conf |= olsTriggerStart
		}
		b = append(b, olsLong(olsCmdTriggerMask+byte(4*stage), p.mask[stage])...)
		b = append(b, olsLong(olsCmdTriggerValue+byte(4*stage), p.value[stage])...)
		b = append(b, olsLong(olsCmdTriggerConf+byte(4*stage), conf)...)
	}
	b = append(b, olsLong(olsCmdDivider, p.divider)...)
	size := uint32(p.readCount/4-1) | uint32(p.delay/4-1)<<16
	b = append(b, olsLong(olsCmdCaptureSize, size)...)
	b = append(b, olsLong(olsCmdFlags, p.flags)...)
	return append(b, olsCmdArm)
}

// transferBytes returns the number of bytes the device sends.
func (p *olsCapture) transferBytes() int { return p.readCount * p.groups }

// trace decodes the device transfer. The device sends the newest sample
// first, and only the bytes of enabled groups, lowest group first.
func (p *olsCapture) trace(c capture.Config, raw []byte, triggered bool) (*logic.Trace, error) {
	if len(raw) != p.transferBytes() {
		return nil, fmt.Errorf("ols: got %d sample bytes, want %d", len(raw), p.transferBytes())
	}
	width := 1
	if c.Channels&0xff00 != 0 {
		width = 2
	}
	out := make([]byte, p.samples*width)
	for i := range p.samples {
		in := raw[(p.readCount-1-i)*p.groups:]
		var v uint16
		j := 0
		for g := range 2 {
			if p.flags&(olsFlagGroup0Off<<g) == 0 {
				v |= uint16(in[j]) << (8 * g)
				j++
			}
		}
		v &= c.Channels
		if width == 2 {
			binary.LittleEndian.PutUint16(out[2*i:], v)
		} else {
			out[i] = byte(v)
		}
	}
	triggerAt := 0
	if triggered {
		triggerAt = p.triggerAt
	}
	return &logic.Trace{
		Rate: p.rate, Channels: c.Channels, Source: c.Source, Falling: c.Falling,
		TriggerAt: min(triggerAt, p.samples-1), Triggered: triggered,
		Width: width, Samples: out,
	}, nil
}
