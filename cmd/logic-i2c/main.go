// Command logic-i2c decodes I2C traffic in a logic-capture JSON file.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/davecheney/logic"
)

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("logic-i2c", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "-", "logic-capture JSON file, or - for stdin")
	sda := flags.Int("sda", -1, "SDA input number, required")
	scl := flags.Int("scl", -1, "SCL input number, required")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *sda < 0 || *sda > 15 || *scl < 0 || *scl > 15 {
		return errors.New("-sda and -scl are required, 0 to 15")
	}
	if *sda == *scl {
		return errors.New("-sda and -scl must be different inputs")
	}
	tr, err := readInput(*input, stdin)
	if err != nil {
		return err
	}
	for _, ch := range []int{*sda, *scl} {
		if tr.Channels>>ch&1 == 0 {
			return fmt.Errorf("L%d is not in the capture (channels %#x)", ch, tr.Channels)
		}
	}
	w := bufio.NewWriter(stdout)
	for _, line := range decode(tr, *sda, *scl) {
		fmt.Fprintln(w, line)
	}
	return w.Flush()
}

// decode returns one line for each transaction, from START to STOP.
// A START inside a transaction is a repeated START, Sr.
func decode(tr *logic.Trace, sda, scl int) []string {
	var lines []string
	var tx []string
	var start int
	var bits, n int
	addr := false
	flush := func() {
		lines = append(lines, fmt.Sprintf("%+.9f %s", float64(start-tr.TriggerAt)/tr.Rate.Hz(), strings.Join(tx, " ")))
		tx = nil
	}
	prev := tr.At(0)
	for i := 1; i < tr.Len(); i++ {
		s := tr.At(i)
		d0, d1 := prev>>sda&1, s>>sda&1
		c0, c1 := prev>>scl&1, s>>scl&1
		prev = s
		switch {
		case c0 == 1 && c1 == 1 && d0 != d1:
			// The SCL rise before a START or STOP is not a data bit.
			if n > 1 && tx != nil {
				tx = append(tx, "?")
			}
			bits, n = 0, 0
			if d1 == 0 {
				if tx != nil {
					tx = append(tx, "Sr")
				} else {
					start = i
					tx = []string{"S"}
				}
				addr = true
			} else if tx != nil {
				tx = append(tx, "P")
				flush()
			}
		case c0 == 0 && c1 == 1 && tx != nil:
			bits = bits<<1 | int(d1)
			n++
			if n == 9 {
				b, ack := bits>>1, "A"
				if bits&1 != 0 {
					ack = "N"
				}
				if addr {
					dir := "W"
					if b&1 != 0 {
						dir = "R"
					}
					tx = append(tx, fmt.Sprintf("%02x", b>>1), dir, ack)
					addr = false
				} else {
					tx = append(tx, fmt.Sprintf("%02x", b), ack)
				}
				bits, n = 0, 0
			}
		}
	}
	if tx != nil {
		flush()
	}
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
		fmt.Fprintln(os.Stderr, "logic-i2c:", err)
		os.Exit(1)
	}
}
