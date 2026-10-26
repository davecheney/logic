package logic

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"
)

type fakeOLSPort struct {
	read       []byte
	writes     [][]byte
	raw        []byte
	finish     bool
	neverDone  bool
	readLimit  int
	writeLimit int
	closed     bool
}

func (p *fakeOLSPort) Read(b []byte) (int, error) {
	if len(p.read) == 0 {
		return 0, nil
	}
	if p.readLimit > 0 && len(b) > p.readLimit {
		b = b[:p.readLimit]
	}
	n := copy(b, p.read)
	p.read = p.read[n:]
	return n, nil
}

func (p *fakeOLSPort) Write(b []byte) (int, error) {
	n := len(b)
	if p.writeLimit > 0 && n > p.writeLimit {
		n = p.writeLimit
	}
	b = append([]byte(nil), b[:n]...)
	p.writes = append(p.writes, b)
	if len(b) > 1 && b[len(b)-1] == olsCmdArm && !p.neverDone {
		p.read = append(p.read, p.raw...)
		p.raw = nil
	}
	if len(b) == 1 {
		switch b[0] {
		case olsCmdID:
			p.read = append(p.read, olsID...)
		case olsCmdMetadata:
			metadata, _ := hex.DecodeString(olsDeviceMetadata)
			p.read = append(p.read, metadata...)
		case olsCmdFinish:
			p.finish = true
			if p.neverDone {
				p.read = append(p.read, p.raw...)
				p.raw = nil
			}
		}
	}
	return n, nil
}

func (p *fakeOLSPort) SetReadTimeout(time.Duration) error { return nil }
func (p *fakeOLSPort) ResetInputBuffer() error            { p.read = nil; return nil }
func (p *fakeOLSPort) Drain() error                       { return nil }
func (p *fakeOLSPort) Close() error                       { p.closed = true; return nil }

func TestIdentifyOLS(t *testing.T) {
	p := new(fakeOLSPort)
	got, err := identifyOLS(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Open Logic Sniffer v1.01" || got.FPGA != "3.07" || got.Memory != 24576 {
		t.Fatalf("metadata: %+v", got)
	}
	if !bytes.Equal(p.writes[0], []byte{0, 0, 0, 0, 0}) ||
		!bytes.Equal(p.writes[1], []byte{olsCmdID}) ||
		!bytes.Equal(p.writes[2], []byte{olsCmdMetadata}) {
		t.Fatalf("commands: % x", p.writes)
	}
}

func TestIdentifyOLSRejectsBadID(t *testing.T) {
	p := &fakeOLSPort{read: []byte("bad!")}
	if _, err := identifyOLS(context.Background(), p); err == nil {
		t.Fatal("bad ID: got no error")
	}
}

func TestOLSCapture(t *testing.T) {
	p := &fakeOLSPort{raw: []byte{4, 3, 2, 1}}
	d := &OLS{ID: "test", port: p, device: newDevice(ClassOLS), meta: olsMetadata{Memory: 24576}}
	caps := d.Capabilities()
	var id ResourceID
	var inputs []ResourceID
	for r := range caps.Resources() {
		if r.ID.Index() < 8 {
			inputs = append(inputs, r.ID)
			if r.ID.Index() == 0 {
				id = r.ID
			}
		}
	}
	req := CaptureRequest{Inputs: inputs, Trigger: id, Rate: MegaHertz, Samples: 4}
	tr, err := d.CaptureLogic(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Rate != MegaHertz || tr.Channels != 0xff || tr.Len() != 4 ||
		!tr.Triggered || tr.TriggerAt != 0 {
		t.Fatalf("trace: %+v", tr)
	}
	for i, want := range []byte{1, 2, 3, 4} {
		if got := tr.Samples[i]; got != want {
			t.Errorf("sample %d: got %d, want %d", i, got, want)
		}
	}
}

func TestOLSAutoCapture(t *testing.T) {
	p := &fakeOLSPort{raw: []byte{4, 3, 2, 1}, neverDone: true}
	d := &OLS{ID: "test", port: p, device: newDevice(ClassOLS), meta: olsMetadata{Memory: 24576}}
	caps := d.Capabilities()
	var id ResourceID
	for r := range caps.Resources() {
		if r.ID.Index() == 0 {
			id = r.ID
			break
		}
	}
	req := CaptureRequest{
		Inputs: []ResourceID{id}, Trigger: id, Rate: MegaHertz, Samples: 4, Auto: time.Millisecond,
	}
	tr, err := d.CaptureLogic(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Triggered || tr.TriggerAt != 0 {
		t.Fatalf("auto trace: %+v", tr)
	}
	found := false
	for _, b := range p.writes {
		found = found || bytes.Equal(b, []byte{olsCmdFinish})
	}
	if !found {
		t.Fatal("auto capture did not send finish-now")
	}
}

func TestOLSCancelResetsDevice(t *testing.T) {
	p := &fakeOLSPort{neverDone: true}
	d := &OLS{ID: "test", port: p, device: newDevice(ClassOLS), meta: olsMetadata{Memory: 24576}}
	caps := d.Capabilities()
	var id ResourceID
	for r := range caps.Resources() {
		if r.ID.Index() == 0 {
			id = r.ID
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := d.CaptureLogic(ctx, CaptureRequest{
		Inputs: []ResourceID{id}, Trigger: id, Rate: MegaHertz, Samples: 4,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capture error: %v, want deadline exceeded", err)
	}
	foundReset := false
	for _, b := range p.writes {
		foundReset = foundReset || bytes.Equal(b, []byte{0, 0, 0, 0, 0})
	}
	if !foundReset {
		t.Fatal("cancel did not reset the device")
	}
	p.neverDone = false
	p.raw = []byte{4, 3, 2, 1}
	for r := range caps.Resources() {
		if r.ID.Index() == 0 {
			tr, err := d.CaptureLogic(context.Background(), CaptureRequest{
				Inputs: []ResourceID{r.ID}, Trigger: r.ID, Rate: MegaHertz, Samples: 4,
			})
			if err != nil || tr.Len() != 4 || !tr.Triggered {
				t.Fatalf("capture after abort: trace=%+v err=%v", tr, err)
			}
			return
		}
	}
	t.Fatal("no OLS L0 resource")
}

func TestOLSReadAndWriteFull(t *testing.T) {
	p := &fakeOLSPort{read: []byte{1, 2, 3}, readLimit: 1, writeLimit: 1}
	var got [3]byte
	if err := readOLSFull(context.Background(), p, got[:]); err != nil {
		t.Fatal(err)
	}
	if got != [3]byte{1, 2, 3} {
		t.Fatalf("read: %v", got)
	}
	var out shortWriter
	if err := writeOLSFull(&out, []byte{4, 5, 6}); err != nil || !bytes.Equal(out.b, []byte{4, 5, 6}) {
		t.Fatalf("write: %v %v", out.b, err)
	}
	if err := readOLSFull(context.Background(), emptyOLSPort{}, make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read error: %v, want EOF", err)
	}
}

func TestOLSReadExpiredDeadline(t *testing.T) {
	ctx := olsDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	err := readOLSFull(ctx, emptyOLSPort{}, make([]byte, 1))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read error: %v, want deadline exceeded", err)
	}
}

type olsDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (ctx olsDeadlineContext) Deadline() (time.Time, bool) { return ctx.deadline, true }

type shortWriter struct{ b []byte }

func (w *shortWriter) Write(b []byte) (int, error) {
	w.b = append(w.b, b[0])
	return 1, nil
}

type emptyOLSPort struct{}

func (emptyOLSPort) Read([]byte) (int, error)           { return 0, io.EOF }
func (emptyOLSPort) Write([]byte) (int, error)          { return 0, nil }
func (emptyOLSPort) SetReadTimeout(time.Duration) error { return nil }
func (emptyOLSPort) ResetInputBuffer() error            { return nil }
func (emptyOLSPort) Drain() error                       { return nil }
func (emptyOLSPort) Close() error                       { return nil }
