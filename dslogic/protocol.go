package dslogic

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
)

// Control destinations, from DSView libsigrok4DSL/hardware/DSL/command.h.
const (
	ctlFWVersion = 0
	ctlHWStatus  = 2
	ctlProgB     = 3
	ctlLED       = 5
	ctlIntRdy    = 6
	ctlWordWide  = 7
	ctlStart     = 8
	ctlStop      = 9
	ctlBulkWr    = 10
	ctlI2CReg    = 14
	ctlI2CStatus = 15
)

// HW_STATUS bits.
const (
	statusGPIFDone = 1 << 7
	statusFPGADone = 1 << 6
	statusInitB    = 1 << 5
	statusSysClr   = 1 << 3
)

// FPGA registers.
const (
	regCtr0      = 0x70
	ctr0ForceRdy = 1 << 1 // abort; no data
	hdlVersion   = 0x0e
)

// Mode bits of the setting word.
const (
	modeTrigEn = 1 << 0
	modeHalf   = 1 << 5
	modeQuar   = 1 << 6
	modeSlow   = 1 << 10
)

const (
	hwMaxRate   = 100 * logic.MegaHertz
	minRate     = 50 * logic.KiloHertz
	halfRate    = 200 * logic.MegaHertz
	quarterRate = 400 * logic.MegaHertz
	hwDepth     = 256 << 20 // bits of sample memory
	headerSize  = 512
	packetSize  = 512 // USB 2.0 bulk packet
	atomic      = 64  // samples in one block of one channel
	trigCheckID = 0x55555555
	numStages   = 16
	maxTrigPct  = 90
)

// setting is DSL_setting from DSView libsigrok4DSL/hardware/DSL/dsl.h.
// All fields are naturally aligned, so the C struct has no padding.
type setting struct {
	Sync                     uint32
	ModeHeader, Mode         uint16
	DivHeader, DivL, DivH    uint16
	CountHeader, CntL, CntH  uint16
	TposHeader, TposL, TposH uint16
	TrigGlbHeader, TrigGlb   uint16
	DSOCountHeader           uint16
	DSOCntL, DSOCntH         uint16
	ChEnHeader, ChEnL, ChEnH uint16
	FGainHeader, FGain       uint16
	TrigHeader               uint16
	TrigMask0, TrigMask1     [numStages]uint16
	TrigValue0, TrigValue1   [numStages]uint16
	TrigEdge0, TrigEdge1     [numStages]uint16
	TrigLogic0, TrigLogic1   [numStages]uint16
	TrigCount                [numStages]uint32
	EndSync                  uint32
}

func (s *setting) bytes() []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, s)
	return b.Bytes()
}

// acquisition is a checked logic configuration for one capture.
type acquisition struct {
	set     setting
	rate    float64
	limit   int // requested samples
	actual  int // samples requested from the hardware
	enabled []int
}

// depth returns the sample memory for each enabled channel.
func depth(enabled int) int {
	return (hwDepth / max(1, enabled)) &^ 1023
}

// DSLogicMaxSamples returns the largest sample count for the given number of
// enabled inputs.
func DSLogicMaxSamples(inputs int) int { return depth(inputs) }

// plan checks c and builds the hardware setting. It follows dsl_fpga_arm
// in DSView libsigrok4DSL/hardware/DSL/dsl.c for buffer mode.
func dslogicPlan(c capture.Config) (*acquisition, error) {
	n := bits.OnesCount16(c.Channels)
	switch {
	case c.AnyEdge:
		return nil, errors.New("dslogic: any-edge triggers are not supported")
	case c.Channels == 0:
		return nil, errors.New("dslogic: select at least one input")
	case c.Rate < minRate || c.Rate > quarterRate:
		return nil, fmt.Errorf("dslogic: sample rate must be between %v and %v", minRate, quarterRate)
	case c.Rate >= quarterRate && c.Channels&^0x000f != 0:
		return nil, errors.New("dslogic: 400 MS/s supports only L0 through L3")
	case c.Rate >= halfRate && c.Channels&^0x00ff != 0:
		return nil, errors.New("dslogic: 200 MS/s supports only L0 through L7")
	case c.Source < 0 || c.Source > 15 || c.Channels&(1<<c.Source) == 0:
		return nil, errors.New("dslogic: enable the logic trigger input")
	case c.Samples < atomic || c.Samples > depth(n):
		return nil, fmt.Errorf("dslogic: sample count must be between %d and %d for %d inputs", atomic, depth(n), n)
	case math.IsNaN(c.PreTrigger) || c.PreTrigger < 0 || c.PreTrigger > maxTrigPct/100.0:
		return nil, fmt.Errorf("dslogic: pre-trigger fraction must be between 0 and %g", maxTrigPct/100.0)
	case c.Auto < 0:
		return nil, errors.New("dslogic: auto timeout must not be negative")
	}
	p := &acquisition{limit: c.Samples, actual: (c.Samples + 1023) &^ 1023}
	s := &p.set
	s.Sync, s.EndSync = 0xf5a5f5a5, 0xfa5afa5a
	s.ModeHeader, s.DivHeader, s.CountHeader = 0x0001, 0x0102, 0x0302
	s.TposHeader, s.TrigGlbHeader, s.DSOCountHeader = 0x0502, 0x0701, 0x0802
	s.ChEnHeader, s.FGainHeader, s.TrigHeader = 0x0a02, 0x0c01, 0x40a0

	s.Mode = modeTrigEn
	div := uint32(1)
	switch {
	case c.Rate >= quarterRate:
		s.Mode |= modeQuar
		p.rate = quarterRate.Hz()
	case c.Rate >= halfRate:
		s.Mode |= modeHalf
		p.rate = halfRate.Hz()
	default:
		div = uint32((hwMaxRate + c.Rate - 1) / c.Rate)
		p.rate = hwMaxRate.Hz() / float64(div)
	}
	if math.Ceil(p.rate/1000*float64(n)/8) < 1024 {
		s.Mode |= modeSlow
	}
	// The original DSLogic has a pre-divider of one, so div_h holds only
	// the high bits of the divider.
	s.DivL, s.DivH = uint16(div), uint16(div>>16)
	cnt := uint32(p.actual >> 4)
	s.CntL, s.CntH = uint16(cnt), uint16(cnt>>16)
	s.DSOCntL, s.DSOCntH = uint16(p.actual), uint16(p.actual>>16)
	tpos := max(uint32(c.PreTrigger*float64(c.Samples)), atomic)
	tpos = min(tpos, uint32(depth(n)*maxTrigPct/100))
	s.TposL, s.TposH = uint16(tpos)&0xffc0, uint16(tpos>>16)
	s.TrigGlb = uint16(n&0x1f) << 8 // one simple stage
	s.ChEnL = c.Channels
	for ch := range 16 {
		if c.Channels&(1<<ch) != 0 {
			p.enabled = append(p.enabled, ch)
		}
	}

	// Stage 0 holds the simple trigger: one edge on Source, the other
	// inputs are don't care. Stages 1 to 15 never match.
	mask := ^uint16(1 << c.Source)
	value := uint16(0)
	if !c.Falling {
		value = 1 << c.Source
	}
	edge := uint16(1 << c.Source)
	switch {
	case s.Mode&modeQuar != 0:
		mask, value, edge = quad(mask), quad(value), quad(edge)
	case s.Mode&modeHalf != 0:
		mask, value, edge = dup(mask), dup(value), dup(edge)
	}
	s.TrigMask0[0], s.TrigValue0[0], s.TrigEdge0[0] = mask, value, edge
	s.TrigMask1[0] = 0xffff
	for i := range numStages {
		if i > 0 {
			s.TrigMask0[i], s.TrigMask1[i] = 0xffff, 0xffff
		}
		s.TrigLogic0[i], s.TrigLogic1[i] = 2, 2
	}
	return p, nil
}

func dup(v uint16) uint16  { v &= 0x00ff; return v | v<<8 }
func quad(v uint16) uint16 { v &= 0x000f; return v | v<<4 | v<<8 | v<<12 }

// header is ds_trigger_pos, the first 512 bytes after an acquisition.
type header struct {
	CheckID  uint32
	RealPos  uint32
	RAMSAddr uint32
	RemainL  uint32
	RemainH  uint32
	Status   uint32
}

func parseHeader(b []byte) (header, error) {
	var h header
	if len(b) < binary.Size(h) {
		return h, fmt.Errorf("dslogic: short trigger header, %d bytes", len(b))
	}
	_ = binary.Read(bytes.NewReader(b), binary.LittleEndian, &h)
	if h.CheckID != trigCheckID {
		return h, fmt.Errorf("dslogic: bad trigger header check %#08x", h.CheckID)
	}
	return h, nil
}

// samples returns the number of samples that the device sends after the
// header. The hardware counts to c.actual; remain is the part of that count
// it did not capture.
func (h header) samples(c *acquisition) int {
	remain := uint64(h.RemainH)<<32 | uint64(h.RemainL)
	if remain >= uint64(c.actual) {
		return 0
	}
	return (c.actual - int(remain)) &^ 1023
}

// dataBytes returns the transfer size for n samples.
func dataBytes(n, enabled int) int { return n / atomic * enabled * 8 }

// transpose converts LA_CROSS_DATA to packed 16-bit samples. Each block
// holds one little-endian 64-bit word for each enabled input, in input
// order, and bit k of a word is sample k of the block.
func transpose(dst, src []byte, enabled []int) {
	block := 8 * len(enabled)
	for b := 0; (b+1)*block <= len(src); b++ {
		out := dst[b*atomic*2:]
		for i, ch := range enabled {
			w := binary.LittleEndian.Uint64(src[b*block+8*i:])
			bit := uint16(1) << ch
			for k := range atomic {
				if w&(1<<k) != 0 {
					v := binary.LittleEndian.Uint16(out[2*k:])
					binary.LittleEndian.PutUint16(out[2*k:], v|bit)
				}
			}
		}
	}
}
