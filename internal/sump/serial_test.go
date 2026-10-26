package sump

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type testPort struct {
	bytes.Buffer
	readErr    error
	timeoutErr error
	drainErr   error
}

func (p *testPort) Read(b []byte) (int, error) {
	if p.readErr != nil {
		return 0, p.readErr
	}
	if p.Len() == 0 {
		return 0, nil
	}
	return p.Buffer.Read(b[:1])
}
func (p *testPort) Close() error                       { return nil }
func (p *testPort) Drain() error                       { return p.drainErr }
func (p *testPort) SetReadTimeout(time.Duration) error { return p.timeoutErr }
func (p *testPort) ResetInputBuffer() error            { p.Reset(); return nil }

func TestTokenReply(t *testing.T) {
	for _, reply := range [][]byte{
		{0}, []byte("\x01Logic Pirate (40 MHz)\x00\x022013-06-25\x00\x00"),
		{0x20, 0, 0, 0, 8, 0x40, 8, 0},
	} {
		p := new(testPort)
		p.Write(reply)
		got, err := ReadTokenReply(context.Background(), p)
		if err != nil || !bytes.Equal(got, reply) {
			t.Fatalf("token reply: % x, %v, want % x", got, err, reply)
		}
	}
	for _, reply := range [][]byte{{0x60}, append([]byte{1}, bytes.Repeat([]byte{'x'}, 512)...)} {
		p := new(testPort)
		p.Write(reply)
		if _, err := ReadTokenReply(context.Background(), p); err == nil {
			t.Fatal("invalid token reply accepted")
		}
	}
}

func TestReadErrors(t *testing.T) {
	for _, p := range []*testPort{{readErr: io.ErrUnexpectedEOF}, {timeoutErr: io.ErrClosedPipe}} {
		if err := ReadFull(context.Background(), p, make([]byte, 1)); err == nil {
			t.Fatal("read error ignored")
		}
		if _, err := ReadByteUntil(context.Background(), p, time.Now().Add(time.Second), make([]byte, 1)); err == nil {
			t.Fatal("wait error ignored")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ReadFull(ctx, new(testPort), make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := ReadByteUntil(ctx, new(testPort), time.Now().Add(time.Second), make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel wait: %v", err)
	}
	ready, err := ReadByteUntil(context.Background(), new(testPort), time.Now().Add(-time.Second), make([]byte, 1))
	if err != nil || ready {
		t.Fatalf("expired wait: %t, %v", ready, err)
	}
	if err := WriteCommand(&testPort{drainErr: io.ErrClosedPipe}, []byte{2}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("drain error: %v", err)
	}
}
