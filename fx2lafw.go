// The fx2lafw driver drives eight-channel, 24 MHz Saleae Logic clones
// (USB 0925:3881). These are a Cypress FX2 with no FPGA. The driver loads
// the sigrok fx2lafw firmware into FX2 RAM and streams samples over USB.
//
// The protocol follows the sigrok libsigrok fx2lafw driver. The firmware
// in fx2lafw_firmware comes from sigrok-firmware-fx2lafw; see
// THIRD_PARTY_NOTICES.

package logic

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"slices"
	"time"

	"github.com/google/gousb"
)

//go:embed fx2lafw_firmware/fx2lafw-saleae-logic.fw
var fx2lafwFirmware []byte

const (
	fx2VendorID  = 0x0925
	fx2ProductID = 0x3881
	// The fx2lafw firmware keeps the USB ID and sets these strings.
	fx2Manufacturer = "sigrok"
	fx2Product      = "fx2lafw"

	fx2CmdGetFWVersion = 0xb0
	fx2CmdStart        = 0xb1
	fx2FWMajor         = 1

	fx2Clk48MHz       = 1 << 6
	fx2MaxSampleDelay = 6 * 256
	fx2Endpoint       = 2
	fx2Transfers      = 32
)

var fx2Rates = []Frequency{
	20 * KiloHertz, 25 * KiloHertz, 50 * KiloHertz, 100 * KiloHertz, 200 * KiloHertz, 250 * KiloHertz, 500 * KiloHertz,
	1 * MegaHertz, 2 * MegaHertz, 3 * MegaHertz, 4 * MegaHertz, 6 * MegaHertz, 8 * MegaHertz, 12 * MegaHertz, 16 * MegaHertz, 24 * MegaHertz,
}

// FX2 is an eight-channel, 24 MHz Saleae Logic clone. Do not use it with
// another capture program.
type FX2 struct {
	ID   string
	ctx  *gousb.Context
	dev  *gousb.Device
	cfg  *gousb.Config
	intf *gousb.Interface
	in   *gousb.InEndpoint
	// idle is when the previous acquisition has stopped. The firmware
	// stops only when its EP2 FIFO fills, then it discards the FIFO.
	// Without this wait, the next trace starts with old samples.
	idle time.Time
}

// fx2FIFOBytes is the EP2 FIFO size: four 1024-byte buffers.
const fx2FIFOBytes = 4 * 1024

// OpenFX2 finds the Saleae Logic clone and loads the fx2lafw firmware if
// necessary. The firmware goes into RAM; the EEPROM does not change.
func OpenFX2() (_ *FX2, err error) {
	ctx := gousb.NewContext()
	defer func() {
		if err != nil {
			ctx.Close()
		}
	}()
	dev, err := fx2Find(ctx)
	if err != nil {
		return nil, err
	}
	d := &FX2{ctx: ctx, dev: dev}
	defer func() {
		if err != nil {
			d.closeUSB()
		}
	}()
	dev.ControlTimeout = 3 * time.Second
	if d.cfg, err = dev.Config(1); err != nil {
		return nil, fmt.Errorf("fx2lafw: config: %w", err)
	}
	if d.intf, err = d.cfg.Interface(0, 0); err != nil {
		return nil, fmt.Errorf("fx2lafw: claim interface: %w", err)
	}
	if d.in, err = d.intf.InEndpoint(fx2Endpoint); err != nil {
		return nil, fmt.Errorf("fx2lafw: endpoint: %w", err)
	}
	var fw [2]byte
	if n, err := dev.Control(vendorIn, fx2CmdGetFWVersion, 0, 0, fw[:]); err != nil || n != len(fw) {
		return nil, fmt.Errorf("fx2lafw: firmware version: %d bytes, %v", n, err)
	}
	if fw[0] != fx2FWMajor {
		return nil, fmt.Errorf("fx2lafw: firmware version %d.%d, want %d.x", fw[0], fw[1], fx2FWMajor)
	}
	d.ID = fmt.Sprintf("Saleae Logic clone %04x:%04x, fx2lafw %d.%d", fx2VendorID, fx2ProductID, fw[0], fw[1])
	return d, nil
}

// fx2Find opens the analyser. If the fx2lafw firmware is not running, it
// loads it and waits for the device to enumerate again.
func fx2Find(ctx *gousb.Context) (*gousb.Device, error) {
	dev, err := fx2Open(ctx)
	if err != nil {
		return nil, err
	}
	if dev == nil {
		return nil, fmt.Errorf("fx2lafw: no Saleae Logic clone (%04X:%04X) found", fx2VendorID, fx2ProductID)
	}
	if fx2Loaded(dev) {
		return dev, nil
	}
	log.Printf("fx2lafw: loading FX2 firmware")
	err = loadFirmware(dev, fx2lafwFirmware)
	dev.Close()
	if err != nil {
		return nil, fmt.Errorf("fx2lafw: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		dev, err := fx2Open(ctx)
		if err != nil || dev == nil {
			continue
		}
		if fx2Loaded(dev) {
			return dev, nil
		}
		dev.Close()
	}
	return nil, errors.New("fx2lafw: device did not enumerate after firmware load")
}

// fx2Open opens the only analyser, or returns nil if there is none.
func fx2Open(ctx *gousb.Context) (*gousb.Device, error) {
	devs, err := ctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		return desc.Vendor == fx2VendorID && desc.Product == fx2ProductID
	})
	if len(devs) > 1 {
		for _, d := range devs {
			d.Close()
		}
		return nil, errors.New("fx2lafw: connect only one Saleae Logic clone")
	}
	if len(devs) == 1 {
		return devs[0], nil
	}
	if err != nil {
		return nil, fmt.Errorf("fx2lafw: open: %w", err)
	}
	return nil, nil
}

func fx2Loaded(dev *gousb.Device) bool {
	m, err := dev.Manufacturer()
	if err != nil || m != fx2Manufacturer {
		return false
	}
	p, err := dev.Product()
	return err == nil && p == fx2Product
}

func (d *FX2) closeUSB() error {
	if d.intf != nil {
		d.intf.Close()
	}
	var err error
	if d.cfg != nil {
		err = d.cfg.Close()
	}
	if d.dev != nil {
		err = errors.Join(err, d.dev.Close())
	}
	return errors.Join(err, d.ctx.Close())
}

// String returns the device ID.
func (d *FX2) String() string { return d.ID }

// Close releases the device.
func (d *FX2) Close() error {
	if d.ctx == nil {
		return errors.New("fx2lafw: analyser is closed")
	}
	err := d.closeUSB()
	*d = FX2{ID: d.ID}
	return err
}

// fx2StartCommand returns the CMD_START payload for rate: flags, then the
// sample delay in clock cycles minus one, big endian. The FX2 counts
// either a 48 MHz or a 30 MHz clock.
func fx2StartCommand(rate Frequency) ([3]byte, error) {
	if rate%Hertz != 0 || rate == 0 || rate > 48*MegaHertz {
		return [3]byte{}, fmt.Errorf("fx2lafw: cannot sample at %v", rate)
	}
	r := int(rate / Hertz)
	var flags byte
	delay := 0
	if 48_000_000%r == 0 {
		flags, delay = fx2Clk48MHz, 48_000_000/r-1
		if delay > fx2MaxSampleDelay {
			delay = 0
		}
	}
	if delay == 0 && 30_000_000%r == 0 {
		flags, delay = 0, 30_000_000/r-1
	}
	if delay <= 0 || delay > fx2MaxSampleDelay {
		return [3]byte{}, fmt.Errorf("fx2lafw: cannot sample at %v", rate)
	}
	return [3]byte{flags, byte(delay >> 8), byte(delay)}, nil
}

// ValidateLogic checks a configuration without accessing USB.
func (d *FX2) ValidateLogic(c Config) error {
	switch {
	case c.Channels == 0 || c.Channels&^0xff != 0:
		return errors.New("fx2lafw: select inputs L0 through L7")
	case c.Source < 0 || c.Source > 7 || c.Channels&(1<<c.Source) == 0:
		return errors.New("fx2lafw: enable the logic trigger input")
	case !slices.Contains(fx2Rates, c.Rate):
		return errors.New("fx2lafw: use a supported sample rate from 20000 to 24000000 S/s; see docs/fx2lafw.md")
	case c.Samples < 2:
		return errors.New("fx2lafw: capture at least two samples")
	case math.IsNaN(c.PreTrigger) || c.PreTrigger < 0 || c.PreTrigger >= 1:
		return errors.New("fx2lafw: pre-trigger fraction must be at least 0 and less than 1")
	case c.Auto < 0:
		return errors.New("fx2lafw: auto timeout must not be negative")
	}
	return nil
}

// CaptureLogic streams all eight inputs and keeps the requested trigger
// window. Auto is measured in sampled signal time, not USB startup time.
func (d *FX2) CaptureLogic(ctx context.Context, c Config) (*Trace, error) {
	if err := d.ValidateLogic(c); err != nil {
		return nil, err
	}
	if d.in == nil {
		return nil, errors.New("fx2lafw: analyser is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, err := fx2StartCommand(c.Rate)
	if err != nil {
		return nil, err
	}
	if wait := time.Until(d.idle); wait > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	// Queue about 10 ms of samples in each transfer, as libsigrok does, so
	// the host keeps up with the FX2 FIFO.
	size := (int(c.Rate/(100*Hertz)) + 511) &^ 511
	// The firmware can read EP0BUF before the data stage of CMD_START
	// arrives, and then it uses the previous command's rate. The second
	// command applies the correct rate. Send both before the stream,
	// because the firmware starts to sample when the host asks for data.
	for range 2 {
		if _, err := d.dev.Control(vendorOut, fx2CmdStart, 0, 0, start[:]); err != nil {
			return nil, fmt.Errorf("fx2lafw: start: %w", err)
		}
	}
	stream, err := d.in.NewStream(size, fx2Transfers)
	if err != nil {
		return nil, fmt.Errorf("fx2lafw: stream: %w", err)
	}
	defer func() {
		fx2Drain(stream)
		fill := time.Duration(2 * fx2FIFOBytes / c.Rate.Hz() * float64(time.Second))
		d.idle = time.Now().Add(fill + 10*time.Millisecond)
	}()
	stall := time.Second + 2*time.Duration(float64(size)/c.Rate.Hz()*float64(time.Second))
	r := fx2Reader(func(p []byte) (int, error) {
		rctx, cancel := context.WithTimeout(ctx, stall)
		defer cancel()
		n, err := stream.ReadContext(rctx, p)
		if err != nil && ctx.Err() == nil && rctx.Err() != nil {
			err = errors.New("no data from the analyser")
		}
		return n, err
	})
	tr, err := fx2Read(ctx, r, c)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("fx2lafw: capture: %w", err)
	}
	return tr, nil
}

type fx2Reader func([]byte) (int, error)

func (f fx2Reader) Read(p []byte) (int, error) { return f(p) }

// fx2Drain stops the stream and waits for the queued transfers, which
// complete as the analyser continues to send samples.
func fx2Drain(s *gousb.ReadStream) {
	s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	buf := make([]byte, 64<<10)
	for {
		if _, err := s.ReadContext(ctx, buf); err != nil {
			return
		}
	}
}

func fx2Read(ctx context.Context, r io.Reader, c Config) (*Trace, error) {
	pre := int(float64(c.Samples) * c.PreTrigger)
	data := make([]byte, c.Samples)
	pos, filled, remaining := 0, 0, -1
	var previous byte
	var seen int64
	auto := int64(math.Ceil(c.Auto.Seconds() * c.Rate.Hz()))
	triggered := false
	var buf [32 * 1024]byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(buf[:])
		for _, sample := range buf[:n] {
			sample &= byte(c.Channels)
			data[pos] = sample
			pos = (pos + 1) % len(data)
			seen++
			if remaining < 0 && seen > 1 && filled >= pre {
				mask := byte(1 << c.Source)
				edge := previous&mask == 0 && sample&mask != 0
				if c.Falling {
					edge = previous&mask != 0 && sample&mask == 0
				}
				if edge || (auto > 0 && seen >= auto) {
					triggered = edge
					remaining = c.Samples - pre
				}
			}
			previous = sample
			filled = min(filled+1, c.Samples)
			if remaining >= 0 {
				remaining--
				if remaining == 0 {
					// The ring now holds exactly pre samples before the trigger.
					slices.Reverse(data[:pos])
					slices.Reverse(data[pos:])
					slices.Reverse(data)
					return &Trace{
						Rate: c.Rate.Hz(), Channels: c.Channels, Source: c.Source,
						Falling: c.Falling, TriggerAt: pre, Triggered: triggered,
						Width: 1, Samples: data,
					}, nil
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
}
