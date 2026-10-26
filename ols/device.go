package ols

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
	"github.com/davecheney/logic/internal/sump"
	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

const (
	olsVendorID  = "04d8"
	olsProductID = "fc92"
)

// OLS drives an Open Workbench Logic Sniffer on its USB CDC serial port.
// Use only L0 through L15, the buffered inputs.
type OLS struct {
	ID     string
	port   sump.Port
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
func Open(port string) (*OLS, error) {
	if port == "" {
		var err error
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
	if err := p.SetReadTimeout(sump.ReadTimeout); err != nil {
		return nil, errors.Join(fmt.Errorf("ols: set read timeout: %w", err), p.Close())
	}
	if err := p.ResetInputBuffer(); err != nil {
		return nil, errors.Join(fmt.Errorf("ols: clear input: %w", err), p.Close())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.meta, err = identifyOLS(ctx, d.port)
	if err != nil {
		return nil, errors.Join(err, p.Close())
	}
	d.ID = fmt.Sprintf("%s, Demon Core %s", d.meta.Name, d.meta.FPGA)
	return d, nil
}

func identifyOLS(ctx context.Context, p sump.Port) (olsMetadata, error) {
	var m olsMetadata
	if err := sump.WriteCommand(p, []byte{0, 0, 0, 0, 0}); err != nil {
		return m, fmt.Errorf("ols: reset: %w", err)
	}
	if err := sump.WriteCommand(p, []byte{olsCmdID}); err != nil {
		return m, fmt.Errorf("ols: request ID: %w", err)
	}
	id := make([]byte, len(olsID))
	if err := sump.ReadFull(ctx, p, id); err != nil {
		return m, fmt.Errorf("ols: read ID: %w", err)
	}
	if string(id) != string(olsID) {
		return m, fmt.Errorf("ols: invalid ID %q", id)
	}
	if err := sump.WriteCommand(p, []byte{olsCmdMetadata}); err != nil {
		return m, fmt.Errorf("ols: request metadata: %w", err)
	}
	reply, err := sump.ReadTokenReply(ctx, p)
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
	if err := sump.WriteFull(d.port, []byte{olsCmdReset, olsCmdReset, olsCmdReset, olsCmdReset, olsCmdReset}); err != nil {
		return fmt.Errorf("ols: reset: %w", err)
	}
	return d.port.Drain()
}

func (d *OLS) writeCommand(b []byte) error {
	return sump.WriteCommand(d.port, b)
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
		hasData, err := sump.ReadByteUntil(ctx, d.port, deadline, first[:])
		if err != nil {
			return nil, d.abort(fmt.Errorf("ols: wait for trigger: %w", err))
		}
		if hasData {
			raw[0] = first[0]
			if err := sump.ReadFull(ctx, d.port, raw[1:]); err != nil {
				return nil, d.abort(fmt.Errorf("ols: read capture: %w", err))
			}
		} else {
			if err := d.writeCommand([]byte{olsCmdFinish}); err != nil {
				return nil, d.abort(fmt.Errorf("ols: finish auto capture: %w", err))
			}
			triggered = false
			finishCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := sump.ReadFull(finishCtx, d.port, raw)
			cancel()
			if err != nil {
				return nil, d.abort(fmt.Errorf("ols: read auto capture: %w", err))
			}
		}
	} else if err := sump.ReadFull(ctx, d.port, raw); err != nil {
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

// Ensure serial implementations and tests satisfy the transport contract.
var _ sump.Port = (serial.Port)(nil)
