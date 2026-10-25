// The BitScope driver controls a BitScope Micro (BS05) through the BitScope
// virtual machine (VM) protocol.
//
// The register map and capture mode table come from the BitScope VM
// specification, as given in github.com/jonathanhogg/scopething.

package logic

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// VM registers. Multi-byte registers are little endian.
const (
	regTriggerLogic  = 0x05
	regTriggerMask   = 0x06
	regSpockOption   = 0x07
	regSampleAddress = 0x08 // 24 bit
	regClockScale    = 0x14 // 16 bit
	regDumpRepeat    = 0x16 // 16 bit
	regDumpSend      = 0x18 // 16 bit
	regDumpSkip      = 0x1a // 16 bit
	regDumpCount     = 0x1c // 16 bit
	regDumpMode      = 0x1e
	regTraceMode     = 0x21
	regTraceDelay    = 0x22 // 32 bit
	regTraceIntro    = 0x26 // 16 bit
	regTraceOutro    = 0x2a // 16 bit
	regTimeout       = 0x2c // 16 bit
	regClockTicks    = 0x2e // 16 bit
	regDumpChan      = 0x30
	regBufferMode    = 0x31
	regTriggerIntro  = 0x32 // 16 bit
	regTriggerOutro  = 0x34 // 16 bit
	regAnalogEnable  = 0x37
	regDigitalEnable = 0x38
	regPrelude       = 0x3a // 16 bit
	regConverterLo   = 0x64 // U0.16
	regConverterHi   = 0x66 // U0.16
	regTriggerLevel  = 0x68 // U0.16
	regKitchenSinkA  = 0x7b
	regKitchenSinkB  = 0x7c
)

// ScopeTrace status codes sent at the end of a trace.
const (
	statusDone = 0x00 // trigger seen
	statusAuto = 0x01 // timeout
	statusWait = 0x02 // still waiting
	statusStop = 0x03 // cancelled
)

// Port is the byte link to the device. A read that times out must return
// (0, nil), as go.bug.st/serial does.
type Port interface {
	io.ReadWriter
}

// vm sends commands and reads replies. The device echoes each command
// byte. Text replies end with '\r'.
type vm struct {
	port    Port
	pending []byte
	timeout time.Duration
	trace   bool
}

var errTimeout = errors.New("bitscope: timeout waiting for device")

// fill reads more bytes from the port into the pending buffer.
func (v *vm) fill(deadline time.Time) error {
	var buf [4096]byte
	for {
		if !time.Now().Before(deadline) {
			return errTimeout
		}
		n, err := v.port.Read(buf[:])
		if v.trace && (n > 0 || err != nil) {
			fmt.Printf("scope RX %q error=%v\n", buf[:n], err)
		}
		if n > 0 {
			v.pending = append(v.pending, buf[:n]...)
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// read returns exactly n bytes.
func (v *vm) read(n int, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for len(v.pending) < n {
		if err := v.fill(deadline); err != nil {
			return nil, err
		}
	}
	out := bytes.Clone(v.pending[:n])
	v.pending = v.pending[n:]
	return out, nil
}

// reply returns the next '\r' terminated line, without the terminator.
func (v *vm) reply(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if i := bytes.IndexByte(v.pending, '\r'); i >= 0 {
			line := string(v.pending[:i])
			v.pending = v.pending[i+1:]
			return line, nil
		}
		if len(v.pending) > 256 {
			return "", errors.New("bitscope: text reply is too long")
		}
		if err := v.fill(deadline); err != nil {
			return "", err
		}
	}
}

// hexReply reads a reply and parses it as a hex number.
func (v *vm) hexReply(timeout time.Duration) (uint64, error) {
	line, err := v.reply(timeout)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(line), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("bitscope: bad reply %q: %w", line, err)
	}
	return n, nil
}

// issue sends cmd and checks the echo.
func (v *vm) issue(cmd string) error {
	for len(cmd) > 0 {
		end := len(cmd)
		for i := range len(cmd) {
			if strings.ContainsRune(">UXYZ", rune(cmd[i])) {
				end = i + 1
				break
			}
		}
		if err := v.issuePart(cmd[:end]); err != nil {
			return err
		}
		cmd = cmd[end:]
	}
	return nil
}

func (v *vm) issuePart(cmd string) error {
	n, err := v.write(cmd)
	if err != nil {
		return err
	}
	if n != len(cmd) {
		return io.ErrShortWrite
	}
	echo, err := v.read(len(cmd), v.timeout)
	if err != nil {
		return fmt.Errorf("bitscope: echo of %q: %w", cmd, err)
	}
	if string(echo) != cmd {
		return fmt.Errorf("bitscope: echo %q, want %q", echo, cmd)
	}
	return nil
}

func (v *vm) write(cmd string) (int, error) {
	if v.trace {
		fmt.Printf("scope TX %q\n", cmd)
	}
	n, err := io.WriteString(v.port, cmd)
	if err != nil || n != len(cmd) {
		fmt.Printf("scope TX wrote=%d want=%d error=%v\n", n, len(cmd), err)
	}
	return n, err
}

// reset stops all activity and clears the link. The device replies with
// '!' when the reset is complete.
func (v *vm) reset() error {
	n, err := v.write("!")
	if err != nil {
		return err
	}
	if n != 1 {
		return io.ErrShortWrite
	}
	deadline := time.Now().Add(v.timeout)
	v.pending = v.pending[:0]
	for !bytes.HasSuffix(v.pending, []byte("!")) {
		v.pending = v.pending[:0]
		if err := v.fill(deadline); err != nil {
			return fmt.Errorf("bitscope: reset: %w", err)
		}
	}
	v.pending = v.pending[:0]
	return nil
}

// identify returns the device revision string, for example "BS000500".
func (v *vm) identify() (string, error) {
	if err := v.issue("?"); err != nil {
		return "", err
	}
	// The reply is "\rBS000500\r": an empty line, then the ID.
	if line, err := v.reply(v.timeout); err != nil {
		return "", err
	} else if line != "" {
		return "", fmt.Errorf("bitscope: unexpected ID prefix %q", line)
	}
	return v.reply(v.timeout)
}

// cancelTrace keeps the generator running. The ID reply marks the end
// of any trace replies that were already in flight.
func (v *vm) cancelTrace(id string) error {
	n, err := v.write("K?")
	if err != nil {
		return err
	}
	if n != 2 {
		return io.ErrShortWrite
	}
	deadline := time.Now().Add(v.timeout)
	for {
		if i := bytes.IndexByte(v.pending, '?'); i >= 0 {
			v.pending = v.pending[i+1:]
			break
		}
		v.pending = v.pending[:0]
		if err := v.fill(deadline); err != nil {
			return err
		}
	}
	if line, err := v.reply(v.timeout); err != nil {
		return err
	} else if line != "" {
		return fmt.Errorf("bitscope: unexpected cancel prefix %q", line)
	}
	reply, err := v.reply(v.timeout)
	if err != nil {
		return err
	}
	if reply != id {
		return fmt.Errorf("bitscope: cancel ID %q, want %q", reply, id)
	}
	return nil
}

func (v *vm) register(addr byte) (uint8, error) {
	if err := v.issue(fmt.Sprintf("%02x@p", addr)); err != nil {
		return 0, err
	}
	if line, err := v.reply(v.timeout); err != nil {
		return 0, err
	} else if line != "" {
		return 0, fmt.Errorf("bitscope: unexpected register prefix %q", line)
	}
	line, err := v.reply(v.timeout)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(line, 16, 8)
	if err != nil {
		return 0, fmt.Errorf("bitscope: bad register reply %q: %w", line, err)
	}
	return uint8(value), nil
}

// registers collects register writes into one VM command string.
type registers struct {
	b strings.Builder
}

// set writes the low width bytes of value, little endian, from addr.
// Each byte is written as "AA@VVs": AA selects the address and VV is
// the value to store.
func (r *registers) set(addr byte, width int, value uint32) {
	for i := range width {
		fmt.Fprintf(&r.b, "%02x@%02xs", int(addr)+i, byte(value>>(8*i)))
	}
}

func (r *registers) u8(addr byte, v uint8)   { r.set(addr, 1, uint32(v)) }
func (r *registers) u16(addr byte, v uint16) { r.set(addr, 2, uint32(v)) }
func (r *registers) u24(addr byte, v uint32) { r.set(addr, 3, v) }
func (r *registers) u32(addr byte, v uint32) { r.set(addr, 4, v) }

// frac16 writes a U0.16 fixed point value in the range [0, 1).
func (r *registers) frac16(addr byte, f float64) {
	v := f * 65536
	v = min(max(v, 0), 65535)
	r.u16(addr, uint16(v+0.5))
}

func (r *registers) String() string { return r.b.String() }
