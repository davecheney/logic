// Command logic-edges lists the level changes in a logic-capture JSON file.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/davecheney/logic"
)

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("logic-edges", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "-", "logic-capture JSON file, or - for stdin")
	channels := flags.Uint64("channels", 0, "input bit mask: bit n is Ln (default: all inputs in the capture)")
	from := flags.Duration("from", 0, "window start relative to the trigger, for example -100us (default: first sample)")
	to := flags.Duration("to", 0, "window end relative to the trigger (default: last sample)")
	limit := flags.Int("limit", 0, "stop after this many edges (0 lists all)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *limit < 0 {
		return errors.New("limit must not be negative")
	}
	tr, err := readInput(*input, stdin)
	if err != nil {
		return err
	}
	mask := *channels
	if mask == 0 {
		mask = tr.Mask()
	}
	if mask&^tr.Mask() != 0 {
		return fmt.Errorf("channels %#x are not all in the capture (%#x)", mask, tr.Mask())
	}

	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	lo, hi := 0, tr.Len()
	if set["from"] {
		lo = clamp(tr.TriggerAt+samples(*from, tr.Rate), 0, tr.Len())
	}
	if set["to"] {
		hi = clamp(tr.TriggerAt+samples(*to, tr.Rate)+1, 0, tr.Len())
	}
	if lo >= hi {
		return errors.New("the window holds no samples")
	}

	w := bufio.NewWriter(stdout)
	var levels []string
	for ch := range 64 {
		if mask>>ch&1 != 0 {
			levels = append(levels, fmt.Sprintf("L%d=%d", ch, tr.At(lo)>>ch&1))
		}
	}
	fmt.Fprintf(w, "# sample_rate_hz=%g trigger_sample=%d triggered=%t start=%+.9f %s\n",
		tr.Rate.Hz(), tr.TriggerAt, tr.Triggered, seconds(lo, tr), strings.Join(levels, " "))
	n := 0
	prev := tr.At(lo)
	for i := lo + 1; i < hi; i++ {
		s := tr.At(i)
		d := (s ^ prev) & mask
		prev = s
		for ch := 0; d != 0; ch++ {
			if d&1 != 0 {
				if *limit > 0 && n == *limit {
					return w.Flush()
				}
				fmt.Fprintf(w, "%+.9f L%d %d\n", seconds(i, tr), ch, s>>ch&1)
				n++
			}
			d >>= 1
		}
	}
	return w.Flush()
}

// seconds returns the time of sample i relative to the trigger.
func seconds(i int, tr *logic.Trace) float64 {
	return float64(i-tr.TriggerAt) / tr.Rate.Hz()
}

func samples(d time.Duration, rate logic.Frequency) int {
	return int(d.Seconds() * rate.Hz())
}

func clamp(v, lo, hi int) int {
	return max(lo, min(v, hi))
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
		fmt.Fprintln(os.Stderr, "logic-edges:", err)
		os.Exit(1)
	}
}
