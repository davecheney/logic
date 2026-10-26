package logicpirate

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

// LogicPirate drives a Dangerous Prototypes Logic Pirate on a USB serial port.
type LogicPirate struct {
	port   sump.Port
	device *logic.Device
	meta   metadata
	mu     sync.Mutex
	closed bool
}

// FindPort finds exactly one Logic Pirate (04d8:fa95).
func FindPort() (string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", fmt.Errorf("logicpirate: list serial ports: %w", err)
	}
	var names []string
	for _, p := range ports {
		if p.IsUSB && strings.EqualFold(p.VID, "04d8") && strings.EqualFold(p.PID, "fa95") {
			names = append(names, p.Name)
		}
	}
	switch len(names) {
	case 0:
		return "", errors.New("logicpirate: no Logic Pirate (04d8:fa95) found; use -port")
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("logicpirate: multiple Logic Pirates found; use -port: %s", strings.Join(names, ", "))
	}
}

// Open opens and identifies a Logic Pirate. An empty port searches for one
// connected device. The device ignores the serial baud rate.
func Open(port string) (*LogicPirate, error) {
	if port == "" {
		found, err := FindPort()
		if err != nil {
			return nil, err
		}
		port = found
	}
	p, err := serial.Open(port, &serial.Mode{BaudRate: 115200, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
	if err != nil {
		return nil, fmt.Errorf("logicpirate: open %s: %w", port, err)
	}
	d := &LogicPirate{port: p, device: logic.NewDevice(logic.ClassLogicPirate)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.reset(ctx); err != nil {
		return nil, errors.Join(err, p.Close())
	}
	d.meta, err = identify(ctx, p)
	if err != nil {
		return nil, errors.Join(err, p.Close())
	}
	return d, nil
}

func identify(ctx context.Context, p sump.Port) (metadata, error) {
	var m metadata
	if err := sump.WriteCommand(p, []byte{cmdID}); err != nil {
		return m, fmt.Errorf("logicpirate: request ID: %w", err)
	}
	var id [4]byte
	if err := sump.ReadFull(ctx, p, id[:]); err != nil {
		return m, fmt.Errorf("logicpirate: read ID: %w", err)
	}
	if string(id[:]) != "1ALS" {
		return m, fmt.Errorf("logicpirate: invalid ID %q", id)
	}
	if err := sump.WriteCommand(p, []byte{cmdMetadata}); err != nil {
		return m, fmt.Errorf("logicpirate: request metadata: %w", err)
	}
	reply, err := sump.ReadTokenReply(ctx, p)
	if err != nil {
		return m, fmt.Errorf("logicpirate: read metadata: %w", err)
	}
	return parseMetadata(reply)
}

// String returns the device name and firmware version.
func (d *LogicPirate) String() string {
	return fmt.Sprintf("%s, firmware %s", d.meta.name, d.meta.firmware)
}

// Capabilities returns the inputs and limits of the connected firmware.
func (d *LogicPirate) Capabilities() logic.CapabilitySet {
	return capabilities(d.device, ratesFor(d.meta.clock))
}

// Capabilities returns the inputs and limits without opening hardware. The
// rate list includes both firmware variants; Open selects the actual list.
func Capabilities(dev *logic.Device) logic.CapabilitySet {
	return capabilities(dev, ratesFor(0))
}

func capabilities(dev *logic.Device, settings []rateSetting) logic.CapabilitySet {
	rates := make([]logic.Frequency, len(settings))
	for i, r := range settings {
		rates[i] = r.rate
	}
	limits := make([]logic.SampleLimit, 8)
	for i := range limits {
		limits[i] = logic.SampleLimit{InputCount: i + 1, MaxSamples: maxSamples}
	}
	return logic.NewCapabilitySet(
		logic.DigitalInputs(dev, 8, rates[len(rates)-1], rates[0], rates), nil, limits)
}

// Validate checks a request without opening hardware.
func Validate(dev *logic.Device, req logic.CaptureRequest) error {
	c, err := capture.FromRequest(dev, Capabilities(dev), req)
	if err != nil {
		return err
	}
	_, err = plan(c, ratesFor(0))
	return err
}

// ValidateCapture checks a request against the connected firmware.
func (d *LogicPirate) ValidateCapture(req logic.CaptureRequest) error {
	c, err := capture.FromRequest(d.device, d.Capabilities(), req)
	if err != nil {
		return err
	}
	_, err = plan(c, ratesFor(d.meta.clock))
	return err
}

// ConfigureAWG rejects output requests. The driver has no output resources.
func (d *LogicPirate) ConfigureAWG(req logic.OutputRequest) (logic.Frequency, error) {
	return 0, capture.ValidateOutput(d.device, d.Capabilities(), req)
}

// Close releases the serial port.
func (d *LogicPirate) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.port.Close()
}

// CaptureLogic waits for either edge on the trigger input. If Auto expires,
// it starts a new capture without a trigger.
func (d *LogicPirate) CaptureLogic(ctx context.Context, req logic.CaptureRequest) (*logic.Trace, error) {
	c, err := capture.FromRequest(d.device, d.Capabilities(), req)
	if err != nil {
		return nil, err
	}
	p, err := plan(c, ratesFor(d.meta.clock))
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.closed {
		return nil, errors.New("logicpirate: device is closed")
	}
	if err := sump.WriteCommand(d.port, p.commands(c.Source, true)); err != nil {
		return nil, d.abort(fmt.Errorf("logicpirate: configure capture: %w", err))
	}
	raw := make([]byte, p.readCount)
	triggered := true
	if c.Auto > 0 {
		post := time.Duration(float64(p.readCount) / p.rate.Hz() * float64(time.Second))
		ready, err := sump.ReadByteUntil(ctx, d.port, time.Now().Add(c.Auto).Add(post), raw[:1])
		if err != nil {
			return nil, d.abort(fmt.Errorf("logicpirate: wait for trigger: %w", err))
		}
		if ready {
			err = sump.ReadFull(ctx, d.port, raw[1:])
		} else {
			triggered = false
			// There is no SUMP finish command. Stop, then acquire a new buffer.
			if err = d.reset(ctx); err == nil {
				err = sump.WriteCommand(d.port, p.commands(c.Source, false))
			}
			if err == nil {
				err = sump.ReadFull(ctx, d.port, raw)
			}
		}
		if err != nil {
			return nil, d.abort(fmt.Errorf("logicpirate: read capture: %w", err))
		}
	} else if err := sump.ReadFull(ctx, d.port, raw); err != nil {
		return nil, d.abort(fmt.Errorf("logicpirate: read capture: %w", err))
	}
	tr, err := p.trace(c, raw, triggered)
	if err != nil {
		return nil, err
	}
	tr.Inputs = d.device.TraceInputs(uint64(c.Channels))
	tr.Trigger = d.device.ResourceID(logic.KindDigitalIn, uint(c.Source))
	return tr, nil
}

func (d *LogicPirate) abort(cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return errors.Join(cause, d.reset(ctx))
}

func (d *LogicPirate) reset(ctx context.Context) error {
	if err := sump.WriteCommand(d.port, []byte{cmdReset, cmdReset, cmdReset, cmdReset, cmdReset}); err != nil {
		return fmt.Errorf("logicpirate: reset: %w", err)
	}
	// Reset starts a delayed RAM fill. Drain pending samples and allow the
	// fill to finish before another arm, or it can overwrite that capture.
	settle, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var discard [1024]byte
	for {
		err := sump.ReadFull(settle, d.port, discard[:])
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			if err := d.port.ResetInputBuffer(); err != nil {
				return fmt.Errorf("logicpirate: clear input: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("logicpirate: settle reset: %w", err)
		}
	}
}
