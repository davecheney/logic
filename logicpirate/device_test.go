package logicpirate

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capturetest"
)

type fakePort struct {
	read        []byte
	pending     []byte
	commands    [][]byte
	readCount   int
	mask        uint32
	waitForEdge bool
	badID       bool
	writeErr    error
	readErr     error
	closed      bool
	limit       int
}

func (p *fakePort) Read(b []byte) (int, error) {
	if p.readErr != nil {
		return 0, p.readErr
	}
	if p.limit > 0 {
		b = b[:min(len(b), p.limit)]
	}
	n := copy(b, p.read)
	p.read = p.read[n:]
	return n, nil
}

func (p *fakePort) Write(b []byte) (int, error) {
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	n := len(b)
	if p.limit > 0 {
		n = min(n, p.limit)
	}
	p.pending = append(p.pending, b[:n]...)
	for len(p.pending) > 0 {
		size := 1
		if p.pending[0]&0x80 != 0 {
			size = 5
		}
		if len(p.pending) < size {
			break
		}
		cmd := bytes.Clone(p.pending[:size])
		p.pending = p.pending[size:]
		p.commands = append(p.commands, cmd)
		switch cmd[0] {
		case cmdReset:
			p.read = nil
		case cmdID:
			if p.badID {
				p.read = append(p.read, "bad!"...)
			} else {
				p.read = append(p.read, "1ALS"...)
			}
		case cmdMetadata:
			p.read = append(p.read, []byte("\x01Logic Pirate (40 MHz)\x00\x022013-06-25\x00\x00")...)
		case cmdCaptureSize:
			p.readCount = (int(binary.LittleEndian.Uint16(cmd[1:3])) + 2) * 4
		case cmdTriggerMask:
			p.mask = binary.LittleEndian.Uint32(cmd[1:])
		case cmdArm:
			if p.mask == 0 || !p.waitForEdge {
				for i := range p.readCount {
					p.read = append(p.read, byte(i))
				}
			}
		}
	}
	return n, nil
}

func (p *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (p *fakePort) ResetInputBuffer() error            { p.read = nil; return nil }
func (p *fakePort) Drain() error                       { return nil }
func (p *fakePort) Close() error                       { p.closed = true; return nil }

func device(p *fakePort) *LogicPirate {
	return &LogicPirate{
		port: p, device: logic.NewDevice(logic.ClassLogicPirate),
		meta: metadata{name: "Logic Pirate (40 MHz)", firmware: "2013-06-25", clock: 40},
	}
}

func TestIdentify(t *testing.T) {
	p := &fakePort{limit: 1}
	m, err := identify(context.Background(), p)
	if err != nil || m.clock != 40 || m.firmware != "2013-06-25" {
		t.Fatalf("identify: %+v, %v", m, err)
	}
	if !slices.EqualFunc(p.commands, [][]byte{{cmdID}, {cmdMetadata}}, bytes.Equal) {
		t.Fatalf("identify commands: % x", p.commands)
	}
	if _, err := identify(context.Background(), &fakePort{badID: true}); err == nil {
		t.Fatal("bad ID accepted")
	}
	for _, p := range []*fakePort{{writeErr: io.ErrClosedPipe}, {readErr: io.EOF}} {
		if _, err := identify(context.Background(), p); err == nil {
			t.Fatal("I/O error ignored")
		}
	}
}

func TestCapture(t *testing.T) {
	p := &fakePort{limit: 3}
	d := device(p)
	c := config()
	c.Samples = 4097
	req := capturetest.Request(d, c)
	tr, err := d.CaptureLogic(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Len() != 4097 || tr.Mask() != 0x81 || tr.Rate != c.Rate || !tr.AnyEdge ||
		tr.Falling || !tr.Triggered || tr.Trigger != req.Trigger || tr.TriggerAt != 0 {
		t.Fatalf("trace: %+v", tr)
	}
	if tr.Inputs[0].Device() != d.device || tr.Inputs[7].Device() != d.device {
		t.Fatal("trace uses a different device")
	}
	for i, v := range tr.Samples {
		if v != byte(i)&0x81 {
			t.Fatalf("sample %d: got %#x", i, v)
		}
	}
	if len(p.read) != 0 {
		t.Fatalf("left %d transfer bytes unread", len(p.read))
	}
	if _, err := d.CaptureLogic(context.Background(), req); err != nil {
		t.Fatalf("second capture: %v", err)
	}
}

func TestAutoCapture(t *testing.T) {
	p := &fakePort{waitForEdge: true}
	d := device(p)
	c := config()
	c.Auto = time.Millisecond
	tr, err := d.CaptureLogic(context.Background(), capturetest.Request(d, c))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Triggered || !tr.AnyEdge || tr.TriggerAt != 0 || tr.Len() != c.Samples {
		t.Fatalf("auto trace: %+v", tr)
	}
	if p.mask != 0 || !slices.ContainsFunc(p.commands, func(b []byte) bool { return b[0] == cmdReset }) {
		t.Fatalf("auto capture did not reset and disable trigger: % x", p.commands)
	}
	// A normal capture must restore the trigger mask.
	p.waitForEdge = false
	c.Auto = 0
	tr, err = d.CaptureLogic(context.Background(), capturetest.Request(d, c))
	if err != nil || !tr.Triggered || p.mask != 0x80 {
		t.Fatalf("capture after auto: %+v, %v, mask %#x", tr, err, p.mask)
	}
}

func TestCancelAndRecover(t *testing.T) {
	p := &fakePort{waitForEdge: true}
	d := device(p)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	req := capturetest.Request(d, config())
	_, err := d.CaptureLogic(ctx, req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel: %v", err)
	}
	if !slices.ContainsFunc(p.commands, func(b []byte) bool { return b[0] == cmdReset }) {
		t.Fatal("cancel did not reset")
	}
	p.waitForEdge = false
	if _, err := d.CaptureLogic(context.Background(), req); err != nil {
		t.Fatalf("capture after cancel: %v", err)
	}
}

func TestInvalidRequestsDoNotWrite(t *testing.T) {
	p := new(fakePort)
	d := device(p)
	req := capturetest.Request(d, config())
	req.Rate = 30 * logic.MegaHertz
	if err := Validate(d.device, req); err != nil {
		t.Fatalf("offline check should accept either firmware: %v", err)
	}
	if err := d.ValidateCapture(req); err == nil {
		t.Fatal("40 MHz firmware accepted 30 MHz")
	}
	if _, err := d.CaptureLogic(context.Background(), req); err == nil {
		t.Fatal("capture accepted unsupported rate")
	}
	req = capturetest.Request(d, config())
	req.Inputs[0] = logic.NewDevice(logic.ClassLogicPirate).ResourceID(logic.KindDigitalIn, 0)
	if _, err := d.CaptureLogic(context.Background(), req); err == nil {
		t.Fatal("capture accepted another device's input")
	}
	if len(p.commands) != 0 {
		t.Fatal("invalid request accessed port")
	}
}

func TestClosedAndCancelledDoNotArm(t *testing.T) {
	p := new(fakePort)
	d := device(p)
	req := capturetest.Request(d, config())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.CaptureLogic(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("already cancelled: %v", err)
	}
	if err := d.Close(); err != nil || !p.closed {
		t.Fatalf("close: %v, closed %t", err, p.closed)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CaptureLogic(context.Background(), req); err == nil {
		t.Fatal("closed device accepted capture")
	}
	if len(p.commands) != 0 {
		t.Fatal("closed or cancelled capture armed device")
	}
	if _, err := d.ConfigureAWG(logic.OutputRequest{
		Resource: req.Trigger, Kind: logic.KindAWG,
	}); err == nil {
		t.Fatal("accepted AWG output")
	}
}
