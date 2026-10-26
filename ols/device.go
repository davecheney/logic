package ols

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

const (
	olsVendorID    = "04d8"
	olsProductID   = "fc92"
	olsReadTimeout = 50 * time.Millisecond
)

type olsSerial interface {
	io.ReadWriteCloser
	SetReadTimeout(time.Duration) error
	ResetInputBuffer() error
	Drain() error
}

// OLS drives an Open Workbench Logic Sniffer on its USB CDC serial port.
// Use only L0 through L15, the buffered inputs.
type OLS struct {
	ID     string
	port   olsSerial
	device *logic.Device
	meta   olsMetadata
	mu     sync.Mutex
	closed bool
}

// FindOLSPort finds the serial port for one Open Logic Sniffer (04D8:FC92).
// It returns an error if there are zero or multiple matching devices.
func FindPort() (string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", fmt.Errorf("ols: list serial ports: %w", err)
	}
	var names []string
	for _, p := range ports {
		if p.IsUSB && strings.EqualFold(p.VID, olsVendorID) && strings.EqualFold(p.PID, olsProductID) {
			names = append(names, p.Name)
		}
	}
	switch len(names) {
	case 0:
		return "", errors.New("ols: no Open Logic Sniffer (04D8:FC92) found; use -port")
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("ols: multiple Open Logic Sniffers found; use -port: %s", strings.Join(names, ", "))
	}
}

// OpenOLS opens and identifies an Open Logic Sniffer. An empty port searches
// for one connected device. The serial baud rate is ignored by the device.
func Open(port string) (_ *OLS, err error) {
	if port == "" {
		port, err = FindPort()
		if err != nil {
			return nil, err
		}
	}
	p, err := serial.Open(port, &serial.Mode{BaudRate: 115200, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
	if err != nil {
		return nil, fmt.Errorf("ols: open %s: %w", port, err)
	}
	d := &OLS{port: p, device: logic.NewDevice(logic.ClassOLS)}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.Close())
		}
	}()
	if err := p.SetReadTimeout(olsReadTimeout); err != nil {
		return nil, fmt.Errorf("ols: set read timeout: %w", err)
	}
	if err := p.ResetInputBuffer(); err != nil {
		return nil, fmt.Errorf("ols: clear input: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.meta, err = identifyOLS(ctx, d.port)
	if err != nil {
		return nil, err
	}
	d.ID = fmt.Sprintf("%s, Demon Core %s", d.meta.Name, d.meta.FPGA)
	return d, nil
}

func identifyOLS(ctx context.Context, p olsSerial) (olsMetadata, error) {
	var m olsMetadata
	if err := writeOLSCommand(p, []byte{0, 0, 0, 0, 0}); err != nil {
		return m, fmt.Errorf("ols: reset: %w", err)
	}
	if err := writeOLSCommand(p, []byte{olsCmdID}); err != nil {
		return m, fmt.Errorf("ols: request ID: %w", err)
	}
	id := make([]byte, len(olsID))
	if err := readOLSFull(ctx, p, id); err != nil {
		return m, fmt.Errorf("ols: read ID: %w", err)
	}
	if string(id) != string(olsID) {
		return m, fmt.Errorf("ols: invalid ID %q", id)
	}
	if err := writeOLSCommand(p, []byte{olsCmdMetadata}); err != nil {
		return m, fmt.Errorf("ols: request metadata: %w", err)
	}
	reply, err := readOLSTokenReply(ctx, p)
	if err != nil {
		return m, fmt.Errorf("ols: read metadata: %w", err)
	}
	metadata, err := parseOLSMetadata(reply)
	if err != nil {
		return m, err
	}
	m = *metadata
	if !strings.HasPrefix(m.Name, "Open Logic Sniffer") || m.FPGA == "" ||
		m.Probes < 16 || m.Protocol < 2 || m.Memory < olsMinSamples || m.MaxRate < olsClock {
		return m, fmt.Errorf("ols: unsupported device metadata: %+v", m)
	}
	return m, nil
}

func (d *OLS) reset() error {
	if err := writeOLSFull(d.port, []byte{olsCmdReset, olsCmdReset, olsCmdReset, olsCmdReset, olsCmdReset}); err != nil {
		return fmt.Errorf("ols: reset: %w", err)
	}
	return d.port.Drain()
}

func (d *OLS) writeCommand(b []byte) error {
	return writeOLSCommand(d.port, b)
}

func writeOLSCommand(p olsSerial, b []byte) error {
	if err := writeOLSFull(p, b); err != nil {
		return err
	}
	return p.Drain()
}

// String returns the device name.
func (d *OLS) String() string { return d.ID }

// Close releases the serial port.
func (d *OLS) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.port.Close()
}

// Capabilities returns the supported buffered inputs and capture limits.
func (d *OLS) Capabilities() logic.CapabilitySet { return Capabilities(d.device) }

var olsRates = []logic.Frequency{
	100 * logic.MegaHertz, 50 * logic.MegaHertz, 25 * logic.MegaHertz, 20 * logic.MegaHertz,
	10 * logic.MegaHertz, 5 * logic.MegaHertz, 2 * logic.MegaHertz, logic.MegaHertz,
	500 * logic.KiloHertz, 200 * logic.KiloHertz, 100 * logic.KiloHertz, 50 * logic.KiloHertz,
	10 * logic.KiloHertz,
}

func Capabilities(dev *logic.Device) logic.CapabilitySet {
	limits := make([]logic.SampleLimit, 16)
	for i := range limits {
		n := i + 1
		maxSamples := olsMemoryBytes
		if n > 1 {
			// With multiple inputs, the request can span two channel groups.
			// Report the safe limit for any selection of this size.
			maxSamples /= 2
		}
		limits[i] = logic.SampleLimit{InputCount: n, MaxSamples: maxSamples}
	}
	return logic.NewCapabilitySet(logic.DigitalInputs(dev, 16, olsMinRate(), olsClock, olsRates), nil, limits)
}

// ValidateCapture checks a capture request without accessing the serial port.
func (d *OLS) ValidateCapture(req logic.CaptureRequest) error {
	c, err := capture.FromRequest(d.device, d.Capabilities(), req)
	if err != nil {
		return err
	}
	return d.validateLogic(c)
}

func Validate(dev *logic.Device, req logic.CaptureRequest) error {
	c, err := capture.FromRequest(dev, Capabilities(dev), req)
	if err != nil {
		return err
	}
	return validateOLSLogic(c)
}

func validateOLSLogic(c capture.Config) error {
	_, err := olsPlan(c, olsMemoryBytes)
	return err
}

func (d *OLS) validateLogic(c capture.Config) error {
	_, err := olsPlan(c, d.meta.Memory)
	return err
}

// ConfigureAWG rejects output requests because the OLS has no supported
// output resources.
func (d *OLS) ConfigureAWG(req logic.OutputRequest) (logic.Frequency, error) {
	return 0, capture.ValidateOutput(d.device, d.Capabilities(), req)
}

// CaptureLogic captures the requested inputs and waits for the trigger. When
// Auto is set, it ends a capture that has not triggered after the timeout.
func (d *OLS) CaptureLogic(ctx context.Context, req logic.CaptureRequest) (*logic.Trace, error) {
	c, err := capture.FromRequest(d.device, d.Capabilities(), req)
	if err != nil {
		return nil, err
	}
	return d.captureLogic(ctx, c)
}

func (d *OLS) captureLogic(ctx context.Context, c capture.Config) (*logic.Trace, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.closed {
		return nil, errors.New("ols: device is closed")
	}
	p, err := olsPlan(c, d.meta.Memory)
	if err != nil {
		return nil, err
	}
	if err := d.writeCommand(p.commands()); err != nil {
		return nil, d.abort(fmt.Errorf("ols: configure capture: %w", err))
	}
	raw := make([]byte, p.transferBytes())
	triggered := true
	if c.Auto > 0 {
		captureTime := time.Duration(float64(p.readCount) / p.rate.Hz() * float64(time.Second))
		deadline := time.Now().Add(c.Auto).Add(captureTime)
		var first [1]byte
		hasData, err := readOLSByteUntil(ctx, d.port, deadline, first[:])
		if err != nil {
			return nil, d.abort(fmt.Errorf("ols: wait for trigger: %w", err))
		}
		if hasData {
			raw[0] = first[0]
			if err := readOLSFull(ctx, d.port, raw[1:]); err != nil {
				return nil, d.abort(fmt.Errorf("ols: read capture: %w", err))
			}
		} else {
			if err := d.writeCommand([]byte{olsCmdFinish}); err != nil {
				return nil, d.abort(fmt.Errorf("ols: finish auto capture: %w", err))
			}
			triggered = false
			finishCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := readOLSFull(finishCtx, d.port, raw)
			cancel()
			if err != nil {
				return nil, d.abort(fmt.Errorf("ols: read auto capture: %w", err))
			}
		}
	} else if err := readOLSFull(ctx, d.port, raw); err != nil {
		return nil, d.abort(fmt.Errorf("ols: read capture: %w", err))
	}
	tr, err := p.trace(c, raw, triggered)
	if err != nil {
		return nil, err
	}
	tr.Inputs = d.device.TraceInputs(uint64(c.Channels))
	tr.Trigger = d.device.ResourceID(logic.KindDigitalIn, uint(c.Source))
	return tr, nil
}

func (d *OLS) abort(cause error) error {
	resetErr := d.reset()
	bufferErr := d.port.ResetInputBuffer()
	return errors.Join(cause, resetErr, bufferErr)
}

func waitOLS(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func readOLSFull(ctx context.Context, r olsSerial, b []byte) error {
	for len(b) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		timeout := olsReadTimeout
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return context.DeadlineExceeded
			}
			timeout = min(timeout, remaining)
		}
		if err := r.SetReadTimeout(timeout); err != nil {
			return fmt.Errorf("set read timeout: %w", err)
		}
		n, err := r.Read(b)
		b = b[n:]
		if len(b) == 0 {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if n == 0 {
			if err := waitOLS(ctx, time.Millisecond); err != nil {
				return err
			}
		}
	}
	return nil
}

func readOLSByteUntil(ctx context.Context, r olsSerial, deadline time.Time, b []byte) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		if err := r.SetReadTimeout(min(olsReadTimeout, remaining)); err != nil {
			return false, fmt.Errorf("set read timeout: %w", err)
		}
		n, err := r.Read(b)
		if n > 0 {
			return true, nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if time.Until(deadline) <= 0 {
				return false, nil
			}
			return false, err
		}
		if err := waitOLS(ctx, time.Millisecond); err != nil {
			return false, err
		}
	}
}

func readOLSTokenReply(ctx context.Context, r olsSerial) ([]byte, error) {
	var reply []byte
	var one [1]byte
	for len(reply) < 512 {
		if err := readOLSFull(ctx, r, one[:]); err != nil {
			return nil, err
		}
		key := one[0]
		reply = append(reply, key)
		if key == 0 {
			return reply, nil
		}
		var n int
		switch key >> 5 {
		case 0:
			for {
				if err := readOLSFull(ctx, r, one[:]); err != nil {
					return nil, err
				}
				reply = append(reply, one[0])
				if one[0] == 0 {
					break
				}
				if len(reply) >= 512 {
					return nil, errors.New("token reply exceeds 512 bytes")
				}
			}
		case 1:
			n = 4
		case 2:
			n = 1
		default:
			return nil, fmt.Errorf("token reply has unknown key type %#02x", key)
		}
		if n != 0 {
			if len(reply)+n > 512 {
				return nil, errors.New("token reply exceeds 512 bytes")
			}
			data := make([]byte, n)
			if err := readOLSFull(ctx, r, data); err != nil {
				return nil, err
			}
			reply = append(reply, data...)
		}
	}
	return nil, errors.New("token reply exceeds 512 bytes")
}

func writeOLSFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		b = b[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// Ensure serial implementations and tests satisfy the transport contract.
var _ olsSerial = (serial.Port)(nil)
