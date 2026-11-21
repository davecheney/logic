// Command logic-spi decodes SPI traffic in a logic-capture JSON file.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/davecheney/logic"
)

type config struct {
	sck, sdo, sdi, cs int
	mode              int
	lsb               bool
	gap               time.Duration
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("logic-spi", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "-", "logic-capture JSON file, or - for stdin")
	var c config
	flags.IntVar(&c.sck, "sck", -1, "SCK input number, required")
	flags.IntVar(&c.sdo, "sdo", -1, "SDO (MOSI) input number")
	flags.IntVar(&c.sdi, "sdi", -1, "SDI (MISO) input number")
	flags.IntVar(&c.cs, "cs", -1, "CS input number, active low")
	flags.IntVar(&c.mode, "mode", 0, "SPI mode, 0 to 3")
	flags.BoolVar(&c.lsb, "lsb", false, "bytes are LSB first")
	flags.DurationVar(&c.gap, "gap", 0, "without -cs, idle SCK time that ends a frame (default: 10 SCK periods)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if c.sck < 0 || c.sck > 63 {
		return errors.New("-sck is required, 0 to 63")
	}
	if c.sdo < 0 && c.sdi < 0 {
		return errors.New("-sdo or -sdi is required")
	}
	if c.mode < 0 || c.mode > 3 {
		return errors.New("-mode must be 0 to 3")
	}
	if c.gap < 0 {
		return errors.New("gap must not be negative")
	}
	if c.gap > 0 && c.cs >= 0 {
		return errors.New("-gap is only for captures without -cs")
	}
	used := []int{c.sck}
	for _, ch := range []int{c.sdo, c.sdi, c.cs} {
		if ch < -1 || ch > 63 {
			return errors.New("input numbers must be 0 to 63")
		}
		if ch >= 0 {
			if slices.Contains(used, ch) {
				return fmt.Errorf("L%d is given more than once", ch)
			}
			used = append(used, ch)
		}
	}
	tr, err := readInput(*input, stdin)
	if err != nil {
		return err
	}
	for _, ch := range used {
		if tr.Mask()>>ch&1 == 0 {
			return fmt.Errorf("L%d is not in the capture (channels %#x)", ch, tr.Mask())
		}
	}
	w := bufio.NewWriter(stdout)
	for _, line := range decode(tr, c) {
		fmt.Fprintln(w, line)
	}
	return w.Flush()
}

// frame is the samples from start to end, and the sampling edges in it.
type frame struct {
	start, end int
	edges      []int
	idle       int // SCK level when CS goes low, or -1
}

// sampleEdges returns the sample of each SCK edge that latches data.
// Modes 0 and 3 latch on the rising edge, modes 1 and 2 on the falling edge.
func sampleEdges(tr *logic.Trace, c config) []int {
	latch := uint64(1)
	if c.mode == 1 || c.mode == 2 {
		latch = 0
	}
	var edges []int
	prev := tr.At(0) >> c.sck & 1
	for i := 1; i < tr.Len(); i++ {
		s := tr.At(i) >> c.sck & 1
		if s != prev && s == latch {
			edges = append(edges, i)
		}
		prev = s
	}
	return edges
}

func median(v []int) int {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	return s[len(s)/2]
}

func periods(edges []int) []int {
	var p []int
	for i := 1; i < len(edges); i++ {
		p = append(p, edges[i]-edges[i-1])
	}
	return p
}

// frames splits the edges at CS, or at idle gaps when there is no CS.
func frames(tr *logic.Trace, c config) []frame {
	edges := sampleEdges(tr, c)
	var fs []frame
	if c.cs < 0 {
		gap := int(c.gap.Seconds() * tr.Rate.Hz())
		if c.gap == 0 {
			gap = 10 * median(periods(edges))
		}
		for _, e := range edges {
			if n := len(fs); n > 0 && e-fs[n-1].end <= gap {
				fs[n-1].edges = append(fs[n-1].edges, e)
				fs[n-1].end = e
				continue
			}
			fs = append(fs, frame{start: e, end: e, edges: []int{e}, idle: -1})
		}
		return fs
	}
	cs := func(i int) uint64 { return tr.At(i) >> c.cs & 1 }
	sck := func(i int) int { return int(tr.At(i) >> c.sck & 1) }
	var cur *frame
	if cs(0) == 0 {
		cur = &frame{idle: -1}
	}
	k := 0
	for i := 1; i < tr.Len(); i++ {
		switch {
		case cs(i-1) == 1 && cs(i) == 0:
			cur = &frame{start: i, idle: sck(i)}
		case cs(i-1) == 0 && cs(i) == 1 && cur != nil:
			cur.end = i
			for k < len(edges) && edges[k] < i {
				if edges[k] >= cur.start {
					cur.edges = append(cur.edges, edges[k])
				}
				k++
			}
			fs = append(fs, *cur)
			cur = nil
		}
	}
	if cur != nil {
		cur.end = tr.Len()
		for _, e := range edges[k:] {
			if e >= cur.start {
				cur.edges = append(cur.edges, e)
			}
		}
		fs = append(fs, *cur)
	}
	return fs
}

// decode returns one line for each frame.
func decode(tr *logic.Trace, c config) []string {
	cpol := c.mode >> 1
	var lines []string
	for _, f := range frames(tr, c) {
		var b strings.Builder
		fmt.Fprintf(&b, "%+.9f bits=%d", float64(f.start-tr.TriggerAt)/tr.Rate.Hz(), len(f.edges))
		p := periods(f.edges)
		m := median(p)
		maxp, sum, n := 0, 0, 0
		for _, v := range p {
			maxp = max(maxp, v)
			// Average only the periods within 25% of the median, not the gaps.
			if 4*v <= 5*m {
				sum += v
				n++
			}
		}
		if n > 0 {
			fmt.Fprintf(&b, " sck=%.3fMHz", tr.Rate.Hz()*float64(n)/float64(sum)/1e6)
		}
		unstable := 0
		for _, line := range []struct {
			name string
			ch   int
		}{{"SDO", c.sdo}, {"SDI", c.sdi}} {
			if line.ch < 0 {
				continue
			}
			b.WriteString(" " + line.name)
			var v, nbits int
			for _, e := range f.edges {
				d := int(tr.At(e) >> line.ch & 1)
				if d != int(tr.At(e-1)>>line.ch&1) {
					unstable++
				}
				if c.lsb {
					v |= d << nbits
				} else {
					v = v<<1 | d
				}
				nbits++
				if nbits == 8 {
					fmt.Fprintf(&b, " %02x", v)
					v, nbits = 0, 0
				}
			}
			if nbits != 0 {
				b.WriteString(" ?")
			}
		}
		if 2*maxp > 3*m {
			fmt.Fprintf(&b, " gap=%v", time.Duration(float64(maxp)/tr.Rate.Hz()*1e9).Round(time.Nanosecond))
		}
		if unstable > 0 {
			fmt.Fprintf(&b, " unstable=%d", unstable)
		}
		if f.idle >= 0 && f.idle != cpol {
			fmt.Fprintf(&b, " idle=%d", f.idle)
		}
		lines = append(lines, b.String())
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
		fmt.Fprintln(os.Stderr, "logic-spi:", err)
		os.Exit(1)
	}
}
