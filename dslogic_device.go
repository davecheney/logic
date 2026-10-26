package logic

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/gousb"
)

const (
	vendorID = 0x2a0e
	// productID is the original DSLogic, before and after firmware load.
	productID     = 0x0001
	manufacturer  = "DreamSourceLab"
	loadedProduct = "USB-based DSL Instrument v2"

	reqCtlWr    = 0xb0
	reqCtlRdPre = 0xb1
	reqCtlRd    = 0xb2

	vendorOut = gousb.ControlVendor | gousb.ControlDevice | gousb.ControlOut
	vendorIn  = gousb.ControlVendor | gousb.ControlDevice | gousb.ControlIn
)

// Threshold selects the FPGA bitstream, which sets the input threshold.
type Threshold int

const (
	Threshold3V3 Threshold = iota // 3.3 V logic
	Threshold5V                   // 5 V logic
)

func (t Threshold) String() string {
	if t == Threshold5V {
		return "5V"
	}
	return "3.3V"
}

// DSLogic is an original DSLogic logic analyser.
type DSLogic struct {
	ID        string
	Threshold Threshold
	device    *device
	ctx       *gousb.Context
	dev       *gousb.Device
	cfg       *gousb.Config
	intf      *gousb.Interface
	in        *gousb.InEndpoint
	out       *gousb.OutEndpoint
}

// OpenDSLogic finds the first original DSLogic, loads the FX2 firmware if
// necessary, and configures the FPGA for threshold t.
func OpenDSLogic(t Threshold) (_ *DSLogic, err error) {
	ctx := gousb.NewContext()
	defer func() {
		if err != nil {
			ctx.Close()
		}
	}()
	dev, err := find(ctx)
	if err != nil {
		return nil, err
	}
	d := &DSLogic{ctx: ctx, dev: dev, device: newDevice(ClassDSLogic)}
	defer func() {
		if err != nil {
			d.closeUSB()
		}
	}()
	if err := d.claim(); err != nil {
		return nil, err
	}
	var fw [2]byte
	if err := d.read(ctlFWVersion, 0, fw[:]); err != nil {
		return nil, fmt.Errorf("dslogic: firmware version: %w", err)
	}
	if fw[0] != 2 {
		return nil, fmt.Errorf("dslogic: firmware version %d.%d, want 2.x", fw[0], fw[1])
	}
	serial, _ := dev.SerialNumber()
	d.ID = "DSLogic"
	if serial != "" {
		d.ID += " " + serial
	}
	if err := d.SetThreshold(t); err != nil {
		return nil, err
	}
	return d, nil
}

// find opens the DSLogic. It loads the FX2 firmware and waits for the
// device to enumerate again if the firmware is not loaded.
func find(ctx *gousb.Context) (*gousb.Device, error) {
	dev, err := ctx.OpenDeviceWithVIDPID(vendorID, productID)
	if err != nil {
		return nil, fmt.Errorf("dslogic: open: %w", err)
	}
	if dev == nil {
		return nil, errors.New("dslogic: no DSLogic (2A0E:0001) found")
	}
	if loaded(dev) {
		return dev, nil
	}
	log.Printf("dslogic: loading FX2 firmware")
	err = loadFirmware(dev, dslogicFirmware)
	dev.Close()
	if err != nil {
		return nil, fmt.Errorf("dslogic: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	time.Sleep(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		dev, err := ctx.OpenDeviceWithVIDPID(vendorID, productID)
		if err != nil || dev == nil {
			continue
		}
		if loaded(dev) {
			return dev, nil
		}
		dev.Close()
	}
	return nil, errors.New("dslogic: device did not enumerate after firmware load")
}

func loaded(dev *gousb.Device) bool {
	m, err := dev.Manufacturer()
	if err != nil || m != manufacturer {
		return false
	}
	p, err := dev.Product()
	return err == nil && p == loadedProduct
}

func (d *DSLogic) claim() error {
	d.dev.ControlTimeout = 3 * time.Second
	var err error
	if d.cfg, err = d.dev.Config(1); err != nil {
		return fmt.Errorf("dslogic: config: %w", err)
	}
	if d.intf, err = d.cfg.Interface(0, 0); err != nil {
		return fmt.Errorf("dslogic: claim interface: %w", err)
	}
	if d.out, err = d.intf.OutEndpoint(2); err != nil {
		return err
	}
	d.in, err = d.intf.InEndpoint(6)
	return err
}

func (d *DSLogic) closeUSB() error {
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

// Close turns off the LED and releases the device.
func (d *DSLogic) Close() error {
	_ = d.write(ctlLED, 0, 0)
	return d.closeUSB()
}

func (d *DSLogic) write(dest byte, offset uint16, data ...byte) error {
	b := append([]byte{dest, byte(offset), byte(offset >> 8), byte(len(data))}, data...)
	_, err := d.dev.Control(vendorOut, reqCtlWr, 0, 0, b)
	return err
}

func (d *DSLogic) read(dest byte, offset uint16, data []byte) error {
	b := []byte{dest, byte(offset), byte(offset >> 8), byte(len(data))}
	if _, err := d.dev.Control(vendorOut, reqCtlRdPre, 0, 0, b); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	n, err := d.dev.Control(vendorIn, reqCtlRd, 0, 0, data)
	if err == nil && n != len(data) {
		err = fmt.Errorf("short control read, %d of %d bytes", n, len(data))
	}
	return err
}

// Status reads the HW_STATUS byte.
func (d *DSLogic) Status() (byte, error) {
	var b [1]byte
	err := d.read(ctlHWStatus, 0, b[:])
	return b[0], err
}

// waitStatus polls HW_STATUS until a bit in mask is set.
func (d *DSLogic) waitStatus(mask byte, what string) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		s, err := d.Status()
		if err != nil {
			return err
		}
		if s&mask != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dslogic: timeout waiting for %s (status %#02x)", what, s)
		}
	}
}

func (d *DSLogic) writeReg(addr uint16, v byte) error { return d.write(ctlI2CReg, addr, v) }

// HDLVersion reads the FPGA bitstream version.
func (d *DSLogic) HDLVersion() (byte, error) {
	var b [5]byte
	err := d.read(ctlI2CStatus, 0, b[:])
	return b[4], err
}

// SetThreshold configures the FPGA with the bitstream for t.
func (d *DSLogic) SetThreshold(t Threshold) error {
	bit := fpga33
	if t == Threshold5V {
		bit = fpga50
	}
	steps := []func() error{
		// The bitstream goes over a byte-wide GPIF. A previous session
		// leaves the GPIF word-wide.
		func() error { return d.write(ctlWordWide, 0, 0) },
		func() error { return d.write(ctlProgB, 0, ^byte(1<<2)) },
		func() error { return d.write(ctlLED, 0, 0) },
		func() error { return d.write(ctlProgB, 0, 1<<2) },
		func() error { return d.waitStatus(statusInitB, "FPGA INIT_B") },
		func() error { return d.write(ctlIntRdy, 0, ^byte(0x80)) },
		func() error { return d.write(ctlBulkWr, 0, byte(len(bit)), byte(len(bit)>>8), byte(len(bit)>>16)) },
		func() error { return d.bulkOut(bit) },
		func() error { return d.write(ctlIntRdy, 0, 0x80) },
		func() error { return d.waitStatus(statusGPIFDone, "GPIF done") },
		func() error { return d.write(ctlIntRdy, 0, ^byte(0x80)) },
		func() error { return d.waitStatus(statusFPGADone, "FPGA done") },
		func() error { return d.write(ctlLED, 0, 1) },
		func() error { return d.write(ctlWordWide, 0, 1) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("dslogic: FPGA configuration step %d: %w", i+1, err)
		}
	}
	v, err := d.HDLVersion()
	if err != nil {
		return fmt.Errorf("dslogic: HDL version: %w", err)
	}
	if v != hdlVersion {
		return fmt.Errorf("dslogic: HDL version %#02x, want %#02x", v, hdlVersion)
	}
	d.Threshold = t
	return nil
}

func (d *DSLogic) bulkOut(b []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	n, err := d.out.WriteContext(ctx, b)
	if err == nil && n != len(b) {
		err = fmt.Errorf("short bulk write, %d of %d bytes", n, len(b))
	}
	return err
}

// String returns the device ID.
func (d *DSLogic) String() string { return d.ID }

// dslogicRates are suggested sample rates: the 400 and 200 MHz modes,
// then 100 MHz divided by a whole number.
var dslogicRates = []Frequency{
	400 * MegaHertz, 200 * MegaHertz, 100 * MegaHertz, 50 * MegaHertz,
	25 * MegaHertz, 20 * MegaHertz, 10 * MegaHertz, 5 * MegaHertz,
	2 * MegaHertz, 1 * MegaHertz, 500 * KiloHertz, 200 * KiloHertz,
	100 * KiloHertz, 50 * KiloHertz,
}

// Capabilities returns the 16 inputs of the DSLogic. At 400 MHz only L0
// to L3 work, and at 200 MHz only L0 to L7. The table does not show
// this; ValidateLogic returns an error for those combinations.
func (d *DSLogic) Capabilities() CapabilitySet { return dslogicCapabilities(d.device) }

func dslogicCapabilities(dev *device) CapabilitySet {
	return CapabilitySet{Resources: digitalInputs(dev, 16, minRate, quarterRate, dslogicRates)}
}

// ValidateLogic checks a logic configuration for the DSLogic.
func (d *DSLogic) ValidateLogic(c Config) error {
	_, err := dslogicPlan(c)
	return err
}

// CaptureLogic arms the DSLogic, waits for the trigger, and reads the
// buffer. If c.Auto is not zero and no trigger occurs within c.Auto after
// the buffer can fill, the device captures again without a trigger.
func (d *DSLogic) CaptureLogic(ctx context.Context, c Config) (*Trace, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := dslogicPlan(c)
	if err != nil {
		return nil, err
	}
	wait := time.Duration(0)
	if c.Auto > 0 {
		wait = time.Duration(float64(p.actual)/p.rate*float64(time.Second)) + c.Auto
	}
	h, raw, err := d.acquire(ctx, p, wait)
	auto := errors.Is(err, errNoTrigger)
	if auto {
		p.set.Mode &^= modeTrigEn
		h, raw, err = d.acquire(ctx, p, 0)
	}
	if err != nil {
		return nil, err
	}
	n := len(raw) / len(p.enabled) / 8 * atomic
	samples := make([]byte, 2*n)
	transpose(samples, raw, p.enabled)
	if n > p.limit {
		samples = samples[:2*p.limit]
	}
	tr := &Trace{
		Rate: frequencyFromHz(p.rate), Channels: c.Channels, Source: c.Source, Falling: c.Falling,
		Triggered: !auto && h.Status&1 != 0, Width: 2, Samples: samples,
	}
	if tr.Triggered {
		tr.TriggerAt = int(h.RealPos)
	}
	return tr, nil
}

var errNoTrigger = errors.New("dslogic: no trigger")

// acquire runs one acquisition and returns the header and the raw sample
// data. If wait is not zero and the header does not arrive within wait,
// acquire aborts the acquisition and returns errNoTrigger.
func (d *DSLogic) acquire(ctx context.Context, p *capture, wait time.Duration) (header, []byte, error) {
	if err := d.write(ctlStop, 0); err != nil {
		return header{}, nil, fmt.Errorf("dslogic: stop: %w", err)
	}
	if err := d.arm(p); err != nil {
		return header{}, nil, err
	}
	if err := d.write(ctlStart, 0); err != nil {
		return header{}, nil, fmt.Errorf("dslogic: start: %w", err)
	}
	h, err := d.readHeader(ctx, wait)
	if err != nil {
		return header{}, nil, err
	}
	raw := make([]byte, dataBytes(h.samples(p), len(p.enabled)))
	if err := d.readData(ctx, raw); err != nil {
		d.abort()
		return header{}, nil, err
	}
	return h, raw, nil
}

func (d *DSLogic) arm(p *capture) error {
	set := p.set.bytes()
	words := len(set) / 2
	steps := []func() error{
		func() error { return d.write(ctlWordWide, 0, 1) },
		func() error { return d.write(ctlBulkWr, 0, byte(words), byte(words>>8), byte(words>>16)) },
		func() error { return d.waitStatus(statusSysClr, "SYS_CLR") },
		func() error { return d.bulkOut(set) },
		func() error { return d.write(ctlIntRdy, 0, 0x80) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("dslogic: arm step %d: %w", i+1, err)
		}
	}
	s, err := d.Status()
	if err != nil {
		return fmt.Errorf("dslogic: arm status: %w", err)
	}
	if s&statusGPIFDone == 0 {
		return fmt.Errorf("dslogic: arm not accepted (status %#02x)", s)
	}
	return nil
}

// readHeader reads the trigger header. On error it aborts the acquisition.
func (d *DSLogic) readHeader(ctx context.Context, wait time.Duration) (header, error) {
	rctx, cancel := ctx, context.CancelFunc(func() {})
	if wait > 0 {
		rctx, cancel = context.WithTimeout(ctx, wait)
	}
	defer cancel()
	buf := make([]byte, headerSize)
	n, err := d.in.ReadContext(rctx, buf)
	if err != nil {
		d.abort()
		switch {
		case ctx.Err() != nil:
			return header{}, ctx.Err()
		case rctx.Err() != nil:
			return header{}, errNoTrigger
		}
		return header{}, fmt.Errorf("dslogic: trigger header: %w", err)
	}
	return parseHeader(buf[:n])
}

// readData fills raw with sample data. Reads are whole 512-byte USB
// packets, so the buffer is rounded up; a short packet ends the data.
func (d *DSLogic) readData(ctx context.Context, raw []byte) error {
	const chunk = 1 << 20
	buf := make([]byte, (len(raw)+packetSize-1)/packetSize*packetSize)
	off := 0
	for off < len(raw) {
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		end := min(off+chunk, len(buf))
		n, err := d.in.ReadContext(rctx, buf[off:end])
		cancel()
		off += n
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("dslogic: data read at byte %d of %d: %w", off, len(raw), err)
		}
		if n%packetSize != 0 {
			break
		}
	}
	if off < len(raw) {
		return fmt.Errorf("dslogic: short data, %d of %d bytes", off, len(raw))
	}
	copy(raw, buf)
	return nil
}

// abort ends the acquisition and discards data that is waiting in the
// endpoint, so that the next capture starts clean. After FORCE_RDY the FPGA
// sends fill data until the GPIF stops.
func (d *DSLogic) abort() {
	_ = d.writeReg(regCtr0, ctr0ForceRdy)
	_ = d.write(ctlStop, 0)
	buf := make([]byte, 1<<16)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		n, err := d.in.ReadContext(ctx, buf)
		cancel()
		if err != nil || n == 0 {
			break
		}
	}
}
