package logicpirate

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
)

const (
	cmdReset       = 0x00
	cmdArm         = 0x01
	cmdID          = 0x02
	cmdMetadata    = 0x04
	cmdDivider     = 0x80
	cmdCaptureSize = 0x81
	cmdTriggerMask = 0xc0

	minSamples    = 4096
	transferBlock = 16384
	maxSamples    = 245760 // last transfer block below the full-buffer delay limit
)

type rateSetting struct {
	rate    logic.Frequency
	divider uint32
}

// The firmware maps SUMP's nominal 100 MHz divider to fixed clock settings.
// In particular, the fastest settings are not 100 MHz / (divider + 1).
var commonRates = []rateSetting{
	{10 * logic.MegaHertz, 9}, {5 * logic.MegaHertz, 19}, {2 * logic.MegaHertz, 49},
	{logic.MegaHertz, 99}, {500 * logic.KiloHertz, 199}, {200 * logic.KiloHertz, 499},
	{100 * logic.KiloHertz, 999}, {50 * logic.KiloHertz, 1999}, {20 * logic.KiloHertz, 4999},
	{10 * logic.KiloHertz, 9999}, {5 * logic.KiloHertz, 19999}, {2 * logic.KiloHertz, 49999},
}

func ratesFor(clock int) []rateSetting {
	var rates []rateSetting
	switch clock {
	case 40:
		rates = []rateSetting{{40 * logic.MegaHertz, 1}, {20 * logic.MegaHertz, 4}}
	case 60:
		rates = []rateSetting{{60 * logic.MegaHertz, 0}, {30 * logic.MegaHertz, 2}, {15 * logic.MegaHertz, 5}}
	case 0:
		// Before opening hardware, accept either firmware's rate table.
		rates = []rateSetting{
			{60 * logic.MegaHertz, 0}, {40 * logic.MegaHertz, 1}, {30 * logic.MegaHertz, 2},
			{20 * logic.MegaHertz, 4}, {15 * logic.MegaHertz, 5},
		}
	}
	return append(rates, commonRates...)
}

type metadata struct {
	name     string
	firmware string
	clock    int
}

func parseMetadata(b []byte) (metadata, error) {
	var m metadata
	name, rest, ok := bytes.Cut(b, []byte{0})
	if !ok || len(name) < 2 || name[0] != 0x01 {
		return m, errors.New("logicpirate: invalid device name in metadata")
	}
	version, rest, ok := bytes.Cut(rest, []byte{0})
	if !ok || len(version) < 2 || version[0] != 0x02 || !bytes.Equal(rest, []byte{0}) {
		return m, errors.New("logicpirate: invalid firmware version in metadata")
	}
	m.name, m.firmware = string(name[1:]), string(version[1:])
	switch m.name {
	case "Logic Pirate (40 MHz)":
		m.clock = 40
	case "Logic Pirate (60 MHz)":
		m.clock = 60
	default:
		return m, fmt.Errorf("logicpirate: unsupported device %q", m.name)
	}
	return m, nil
}

type acquisition struct {
	rate      logic.Frequency
	divider   uint32
	readCount int
	samples   int
}

func plan(c capture.Config, rates []rateSetting) (*acquisition, error) {
	switch {
	case c.Channels == 0 || c.Channels&^0xff != 0:
		return nil, errors.New("logicpirate: select inputs L0 through L7")
	case c.Source < 0 || c.Source > 7 || c.Channels&(1<<c.Source) == 0:
		return nil, errors.New("logicpirate: enable the logic trigger input")
	case !c.AnyEdge || c.Falling:
		return nil, errors.New("logicpirate: only any-edge triggers are supported; set AnyEdge or use -any-edge")
	case c.Samples < minSamples || c.Samples > maxSamples:
		return nil, fmt.Errorf("logicpirate: sample count must be between %d and %d", minSamples, maxSamples)
	case c.PreTrigger != 0:
		// The firmware arms at once and does not fill the pre-trigger region first.
		return nil, errors.New("logicpirate: pre-trigger samples are not supported; use a pre-trigger fraction of 0")
	case c.Auto < 0:
		return nil, errors.New("logicpirate: auto timeout must not be negative")
	}
	var setting rateSetting
	found := false
	for _, r := range rates {
		if r.rate == c.Rate {
			setting, found = r, true
			break
		}
	}
	if !found {
		supported := make([]logic.Frequency, len(rates))
		for i, r := range rates {
			supported[i] = r.rate
		}
		return nil, fmt.Errorf("logicpirate: unsupported sample rate %v; use one of %v", c.Rate, supported)
	}
	// Without a final short packet, macOS holds a partial 16 KiB transfer.
	count := (c.Samples + transferBlock - 1) &^ (transferBlock - 1)
	return &acquisition{
		rate: setting.rate, divider: setting.divider,
		readCount: count, samples: c.Samples,
	}, nil
}

func appendLong(b []byte, command byte, v uint32) []byte {
	return binary.LittleEndian.AppendUint32(append(b, command), v)
}

// commands fits in one USB packet and never splits a five-byte command.
func (p *acquisition) commands(source int, triggered bool) []byte {
	var mask uint32
	if triggered {
		mask = 1 << source
	}
	b := appendLong(nil, cmdDivider, p.divider)
	// Unlike the OLS, this firmware adds two to the read count.
	// The delay equals the read count, so the trigger is the first sample.
	size := uint32(p.readCount/4-2) | uint32(p.readCount/4-1)<<16
	b = appendLong(b, cmdCaptureSize, size)
	b = appendLong(b, cmdTriggerMask, mask)
	return append(b, cmdArm)
}

func (p *acquisition) trace(c capture.Config, raw []byte, triggered bool) (*logic.Trace, error) {
	if len(raw) != p.readCount {
		return nil, fmt.Errorf("logicpirate: got %d sample bytes, want %d", len(raw), p.readCount)
	}
	out := make([]byte, p.samples)
	for i := range out {
		out[i] = raw[i] & byte(c.Channels)
	}
	return &logic.Trace{
		Rate: p.rate, AnyEdge: true, Triggered: triggered,
		Width: 1, Samples: out,
	}, nil
}
