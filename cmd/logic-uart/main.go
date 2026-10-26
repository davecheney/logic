// Command logic-uart decodes UART traffic in a logic-capture JSON file.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/davecheney/logic"
)

type config struct {
	line   int
	baud   float64
	parity string
	stop   int
	gap    time.Duration
	hex    bool
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("logic-uart", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "-", "logic-capture JSON file, or - for stdin")
	var c config
	flags.IntVar(&c.line, "line", -1, "input number of the UART line, required")
	flags.Float64Var(&c.baud, "baud", 115200, "bits per second")
	flags.StringVar(&c.parity, "parity", "none", "parity: none, even, or odd")
	flags.IntVar(&c.stop, "stop", 1, "stop bits: 1 or 2")
	flags.DurationVar(&c.gap, "gap", 0, "idle time that ends a line (default: 10 frames)")
	flags.BoolVar(&c.hex, "hex", false, "print bytes in hex instead of quoted text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if c.line < 0 || c.line > 63 {
		return errors.New("-line is required, 0 to 63")
	}
	if !(c.baud > 0) {
		return errors.New("baud must be positive")
	}
	if c.parity != "none" && c.parity != "even" && c.parity != "odd" {
		return errors.New("parity must be none, even, or odd")
	}
	if c.stop != 1 && c.stop != 2 {
		return errors.New("stop must be 1 or 2")
	}
	if c.gap < 0 {
		return errors.New("gap must not be negative")
	}
	tr, err := readInput(*input, stdin)
	if err != nil {
		return err
	}
	if tr.Mask()>>c.line&1 == 0 {
		return fmt.Errorf("L%d is not in the capture (channels %#x)", c.line, tr.Mask())
	}
	if tr.Rate.Hz()/c.baud < 4 {
		return fmt.Errorf("%g S/s is too slow for %g baud, capture at least 4 samples a bit", tr.Rate.Hz(), c.baud)
	}
	w := bufio.NewWriter(stdout)
	for _, line := range decode(tr, c) {
		fmt.Fprintln(w, line)
	}
	return w.Flush()
}

type frame struct {
	start   int
	end     int
	b       byte
	framing bool
	parity  bool
}

// frames finds each frame from its start bit and samples each bit at its centre.
func frames(tr *logic.Trace, c config) []frame {
	bit := tr.Rate.Hz() / c.baud
	nbits := 9 + c.stop
	if c.parity != "none" {
		nbits++
	}
	level := func(i int) int { return int(tr.At(i)>>c.line) & 1 }
	at := func(start, k int) int { return level(start + int((float64(k)+0.5)*bit)) }
	var fs []frame
	for i := 1; i < tr.Len(); i++ {
		if level(i-1) != 1 || level(i) != 0 {
			continue
		}
		end := i + int(float64(nbits)*bit)
		if i+int((float64(nbits)-0.5)*bit) >= tr.Len() {
			break
		}
		if at(i, 0) != 0 {
			continue
		}
		f := frame{start: i, end: end}
		ones := 0
		for k := range 8 {
			v := at(i, 1+k)
			f.b |= byte(v) << k
			ones += v
		}
		k := 9
		if c.parity != "none" {
			want := 0
			if c.parity == "odd" {
				want = 1
			}
			f.parity = (ones+at(i, k))%2 != want
			k++
		}
		for s := range c.stop {
			if at(i, k+s) != 1 {
				f.framing = true
			}
		}
		fs = append(fs, f)
		i = i + int((float64(nbits)-0.5)*bit)
	}
	return fs
}

// decode groups frames into lines that end at a newline or an idle gap.
func decode(tr *logic.Trace, c config) []string {
	gap := int(c.gap.Seconds() * tr.Rate.Hz())
	if c.gap == 0 {
		nbits := 9 + c.stop
		if c.parity != "none" {
			nbits++
		}
		gap = int(10 * float64(nbits) * tr.Rate.Hz() / c.baud)
	}
	var lines []string
	var cur []frame
	flush := func() {
		if len(cur) == 0 {
			return
		}
		data := make([]byte, len(cur))
		var framing, parity []string
		for j, f := range cur {
			data[j] = f.b
			if f.framing {
				framing = append(framing, strconv.Itoa(j))
			}
			if f.parity {
				parity = append(parity, strconv.Itoa(j))
			}
		}
		var text string
		if c.hex {
			text = fmt.Sprintf("% x", data)
		} else {
			text = strconv.Quote(string(data))
		}
		line := fmt.Sprintf("%+.9f %s", float64(cur[0].start-tr.TriggerAt)/tr.Rate.Hz(), text)
		if framing != nil {
			line += " framing=" + strings.Join(framing, ",")
		}
		if parity != nil {
			line += " parity=" + strings.Join(parity, ",")
		}
		lines = append(lines, line)
		cur = nil
	}
	for _, f := range frames(tr, c) {
		if len(cur) > 0 && f.start-cur[len(cur)-1].end > gap {
			flush()
		}
		cur = append(cur, f)
		if f.b == '\n' && !c.hex {
			flush()
		}
	}
	flush()
	return lines
}

func readInput(path string, stdin io.Reader) (*logic.Trace, error) {
	if path == "-" {
		return logic.Read(stdin)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return logic.Read(f)
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "logic-uart:", err)
		os.Exit(1)
	}
}
