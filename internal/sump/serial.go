// Package sump provides serial I/O for SUMP logic analysers.
package sump

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const ReadTimeout = 50 * time.Millisecond

// Port is the serial transport used by the analysers.
type Port interface {
	io.ReadWriteCloser
	SetReadTimeout(time.Duration) error
	ResetInputBuffer() error
	Drain() error
}

// WriteCommand writes all command bytes and drains the serial output.
func WriteCommand(p Port, b []byte) error {
	if err := WriteFull(p, b); err != nil {
		return err
	}
	return p.Drain()
}

// Wait waits for delay or cancellation.
func Wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ReadFull reads b, with short serial timeouts to check cancellation.
func ReadFull(ctx context.Context, r Port, b []byte) error {
	for len(b) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		timeout := ReadTimeout
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
			if err := Wait(ctx, time.Millisecond); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReadByteUntil reads one byte, or returns false when deadline is reached.
func ReadByteUntil(ctx context.Context, r Port, deadline time.Time, b []byte) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		if err := r.SetReadTimeout(min(ReadTimeout, remaining)); err != nil {
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
		if err := Wait(ctx, time.Millisecond); err != nil {
			return false, err
		}
	}
}

// ReadTokenReply reads a SUMP metadata reply, including its end marker.
func ReadTokenReply(ctx context.Context, r Port) ([]byte, error) {
	var reply []byte
	var one [1]byte
	for len(reply) < 512 {
		if err := ReadFull(ctx, r, one[:]); err != nil {
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
				if err := ReadFull(ctx, r, one[:]); err != nil {
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
			if err := ReadFull(ctx, r, data); err != nil {
				return nil, err
			}
			reply = append(reply, data...)
		}
	}
	return nil, errors.New("token reply exceeds 512 bytes")
}

// WriteFull writes b, including when the writer returns short writes.
func WriteFull(w io.Writer, b []byte) error {
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
