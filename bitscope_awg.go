package logic

import (
	"errors"
	"fmt"
	"math"
)

type Waveform string

const (
	Sine     Waveform = "Sine"
	Square   Waveform = "Square"
	Triangle Waveform = "Triangle"
	Saw      Waveform = "Saw"
)

// AWGConfig sets the nominal 0 to 1 V waveform output.
type AWGConfig struct {
	Enabled   bool
	Frequency float64
	Waveform  Waveform
}

// ConfigureAWG configures the BitScope AWG and enables or disables it.
// Call it only between captures. The returned frequency is the programmed
// clock rate, not a measurement.
func (s *BitScope) ConfigureAWG(req OutputRequest) (Frequency, error) {
	if err := validateOutputRequest(s.device, s.Capabilities(), req); err != nil {
		return 0, err
	}
	if !req.Enable {
		_, err := s.SetAWG(AWGConfig{})
		return 0, err
	}
	actual, err := s.SetAWG(AWGConfig{
		Enabled: true, Frequency: req.Frequency.Hz(), Waveform: req.Waveform,
	})
	if err != nil {
		return 0, err
	}
	return frequencyFromHz(actual), nil
}

func awgTiming(frequency float64) (ticks, size int, actual float64, err error) {
	if math.IsNaN(frequency) || math.IsInf(frequency, 0) || frequency < 10 || frequency > 10000 {
		return 0, 0, 0, errors.New("bitscope: AWG frequency must be between 10 and 10000 Hz")
	}
	best := math.Inf(1)
	for n := 50; n <= 1024; n++ {
		clock := int(math.Round(clockRate / (frequency * float64(n))))
		if clock < 40 || clock > 65535 {
			continue
		}
		f := clockRate / float64(clock*n)
		if delta := math.Abs(f - frequency); delta < best || delta == best && n > size {
			ticks, size, actual, best = clock, n, f, delta
		}
	}
	if size == 0 {
		return 0, 0, 0, errors.New("bitscope: no AWG clock setting")
	}
	return
}

func waveValue(w Waveform, phase float64) (float64, error) {
	switch w {
	case Sine:
		return (1 - math.Cos(2*math.Pi*phase)) / 2, nil
	case Square:
		if phase < 0.5 {
			return 1, nil
		}
		return 0, nil
	case Triangle:
		return 1 - math.Abs(2*phase-1), nil
	case Saw:
		return phase, nil
	default:
		return 0, fmt.Errorf("bitscope: invalid waveform %q", w)
	}
}

// SetAWG changes the output. Call it only between captures.
// The returned frequency is the programmed clock rate, not a measurement.
func (s *BitScope) SetAWG(c AWGConfig) (float64, error) {
	var ticks, size int
	var actual float64
	if c.Enabled {
		var err error
		ticks, size, actual, err = awgTiming(c.Frequency)
		if err != nil {
			return 0, err
		}
		if _, err := waveValue(c.Waveform, 0); err != nil {
			return 0, err
		}
	}
	if !c.Enabled {
		return 0, s.StopAWG()
	}
	if err := s.StopAWG(); err != nil {
		return 0, err
	}
	// Write the normalized table. This also permits a full-period saw.
	var table registers
	table.u8(0x46, 0)
	table.u8(0x47, 1)
	table.u16(0x4e, 0)
	table.u16(0x4a, 1)
	if err := s.vm.issue(table.String()); err != nil {
		return 0, err
	}
	for start := 0; start < 1024; start += 64 {
		var batch registers
		for i := start; i < start+64; i++ {
			value, err := waveValue(c.Waveform, float64(i)/1024)
			if err != nil {
				return 0, err
			}
			fmt.Fprintf(&batch.b, "%02xW", uint8(min(255, math.Round(value*256))))
		}
		if err := s.vm.issue(batch.String()); err != nil {
			return 0, fmt.Errorf("bitscope: write AWG table: %w", err)
		}
	}
	if err := s.playAWG(ticks, size); err != nil {
		return 0, err
	}
	return actual, nil
}

// StartSine1kHz starts a nominal 1 kHz sine wave from 0 V to 1 V.
// Do not call it during Capture. Output voltage is not calibrated.
func (s *BitScope) StartSine1kHz() error {
	var synth registers
	synth.u8(0x46, 0)      // command
	synth.u8(0x47, 0)      // sine
	synth.u32(0x5a, 32768) // symmetry, U16.16
	if err := s.vm.issue(synth.String() + "Y"); err != nil {
		return fmt.Errorf("bitscope: make sine table: %w", err)
	}

	return s.playAWG(40, 1000)
}

func (s *BitScope) playAWG(ticks, size int) error {
	sink, err := s.vm.register(regKitchenSinkB)
	if err != nil {
		return err
	}
	var translate registers
	translate.u8(0x46, 0)
	translate.u8(0x47, 0)
	translate.frac16(0x54, 1/3.3)
	offset := int16(math.Round((0.5 - 3.3/2) / 3.3 * 65536))
	translate.u16(0x56, uint16(offset))
	translate.u32(0x5a, uint32(math.Round(1024.0/float64(size)*65536)))
	translate.u16(0x4c, 0)
	translate.u16(0x4e, 0)
	translate.u16(0x4a, uint16(size))
	if err := s.vm.issue(translate.String() + "X"); err != nil {
		return fmt.Errorf("bitscope: set AWG range: %w", err)
	}

	var clock registers
	clock.u8(0x46, 2)
	clock.u8(0x47, 0)
	clock.u16(0x50, uint16(ticks))
	clock.u16(0x52, uint16(size))
	clock.u16(0x5e, 10)
	clock.u16(0x60, 1)
	clock.u16(0x78, 0x7f00)
	clock.u16(0x48, 0x8004)
	if err := s.vm.issue(clock.String() + "Z"); err != nil {
		return fmt.Errorf("bitscope: start AWG clock: %w", err)
	}
	var enable registers
	enable.u8(regKitchenSinkB, sink|0x40)
	if err := s.vm.issue(enable.String() + "U"); err != nil {
		return fmt.Errorf("bitscope: enable AWG output: %w", err)
	}
	return nil
}

// StopAWG stops the waveform generator. Do not call it during Capture.
func (s *BitScope) StopAWG() error {
	sink, err := s.vm.register(regKitchenSinkB)
	if err != nil {
		return err
	}
	var stop registers
	stop.u8(0x46, 1)
	stop.u8(0x47, 0)
	if err := s.vm.issue(stop.String() + "Z"); err != nil {
		return fmt.Errorf("bitscope: stop AWG clock: %w", err)
	}
	var disable registers
	disable.u8(regKitchenSinkB, sink&^0x40)
	return s.vm.issue(disable.String() + "U")
}
