// Command logic-probe checks that a logic analyser responds, without a
// capture.
//
// For a BitScope it sends only the VM reset and identify commands and
// prints the replies. The BS05 uses an FT245R. This chip is a USB to
// parallel FIFO, not a UART, so the device ignores the baud rate.
//
// For a DSLogic it loads the firmware and prints the FPGA status and HDL
// version.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"

	"github.com/davecheney/logic"
)

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("logic-probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	device := flags.String("device", "", "analyser, required: "+strings.Join(logic.Devices(), ", "))
	port := flags.String("port", "", "BitScope serial port (default: search)")
	bauds := flags.String("baud", "460800,115200,230400,921600,57600,19200", "BitScope baud rates to try, in order")
	wait := flags.Duration("wait", 300*time.Millisecond, "BitScope time to wait for a reply")
	threshold := flags.String("threshold", "3.3", "DSLogic input threshold: 3.3 or 5 (volts)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	switch *device {
	case "bitscope":
		if *port == "" {
			p, err := logic.FindPort()
			if err != nil {
				return err
			}
			*port = p
		}
		return probeBitScope(stdout, *port, *bauds, *wait)
	case "dslogic":
		th := logic.Threshold3V3
		switch *threshold {
		case "3.3":
		case "5":
			th = logic.Threshold5V
		default:
			return errors.New("threshold must be 3.3 or 5")
		}
		return probeDSLogic(stdout, th)
	case "fx2lafw", "saleae":
		d, err := logic.OpenFX2()
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, d)
		return d.Close()
	case "":
		return fmt.Errorf("-device is required: %s", strings.Join(logic.Devices(), ", "))
	default:
		return fmt.Errorf("unknown device %q: use %s", *device, strings.Join(logic.Devices(), ", "))
	}
}

func probeDSLogic(w io.Writer, th logic.Threshold) error {
	d, err := logic.OpenDSLogic(th)
	if err != nil {
		return err
	}
	status, err := d.Status()
	if err != nil {
		return errors.Join(err, d.Close())
	}
	hdl, err := d.HDLVersion()
	if err != nil {
		return errors.Join(err, d.Close())
	}
	fmt.Fprintf(w, "%s: threshold %s, HW_STATUS %#02x, HDL version %#02x\n", d, d.Threshold, status, hdl)
	return d.Close()
}

func probeBitScope(w io.Writer, port, bauds string, wait time.Duration) error {
	for _, s := range strings.Split(bauds, ",") {
		baud, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("bad baud rate %q: %v", s, err)
		}
		id, err := probe(w, port, baud, wait)
		if err != nil {
			fmt.Fprintf(w, "%7d baud: %v\n", baud, err)
			continue
		}
		fmt.Fprintf(w, "%7d baud: device ID %q\n", baud, id)
		return nil
	}
	return errors.New("no baud rate gave a valid reply")
}

// probe opens the port at baud, resets the VM, and asks for its ID.
func probe(w io.Writer, name string, baud int, wait time.Duration) (string, error) {
	p, err := serial.Open(name, &serial.Mode{BaudRate: baud, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit})
	if err != nil {
		return "", err
	}
	defer p.Close()
	if err := p.SetReadTimeout(wait); err != nil {
		return "", err
	}
	if err := p.ResetInputBuffer(); err != nil {
		return "", err
	}

	reset, err := command(p, "!", wait)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(w, "%7d baud: reset reply % x %q\n", baud, reset, reset)

	ident, err := command(p, "?", wait)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(w, "%7d baud: identify reply % x %q\n", baud, ident, ident)

	// The device echoes the command byte, then sends the reply.
	s := strings.TrimSpace(string(ident))
	s = strings.TrimPrefix(s, "?")
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "BS") {
		return "", errors.New("no valid ID in reply")
	}
	return s, nil
}

// command writes cmd and reads all bytes that arrive before the port is
// quiet for one read timeout.
func command(p serial.Port, cmd string, wait time.Duration) ([]byte, error) {
	if _, err := io.WriteString(p, cmd); err != nil {
		return nil, err
	}
	var out []byte
	buf := make([]byte, 256)
	deadline := time.Now().Add(5 * wait)
	for time.Now().Before(deadline) {
		n, err := p.Read(buf)
		if err != nil {
			return out, err
		}
		if n == 0 {
			break
		}
		out = append(out, buf[:n]...)
	}
	return out, nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "logic-probe:", err)
		os.Exit(1)
	}
}
