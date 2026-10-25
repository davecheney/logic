package logic

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
)

// Write writes a JSON, CSV, or VCD capture with the actual sample rate
// and trigger position. CSV and VCD times are relative to the first sample.
func Write(w io.Writer, format string, tr *Trace) error {
	if tr == nil || tr.Len() == 0 {
		return fmt.Errorf("logic: no logic trace to export")
	}
	switch format {
	case "json":
		return json.NewEncoder(w).Encode(tr)
	case "csv":
		c := csv.NewWriter(w)
		for _, metadata := range []string{
			fmt.Sprintf("# sample_rate_hz=%s", strconv.FormatFloat(tr.Rate, 'g', -1, 64)),
			fmt.Sprintf("# channels=%d", tr.Channels),
			fmt.Sprintf("# trigger_channel=%d", tr.Source),
			fmt.Sprintf("# falling=%t", tr.Falling),
			fmt.Sprintf("# trigger_sample=%d", tr.TriggerAt),
			fmt.Sprintf("# triggered=%t", tr.Triggered),
		} {
			if err := c.Write([]string{metadata}); err != nil {
				return err
			}
		}
		header := []string{"time_seconds"}
		for ch := range 16 {
			if tr.Channels&(1<<ch) != 0 {
				header = append(header, fmt.Sprintf("L%d", ch))
			}
		}
		if err := c.Write(header); err != nil {
			return err
		}
		row := make([]string, len(header))
		for i := range tr.Len() {
			sample := tr.At(i)
			row[0] = strconv.FormatFloat(float64(i)/tr.Rate, 'g', -1, 64)
			col := 1
			for ch := range 16 {
				if tr.Channels&(1<<ch) != 0 {
					row[col] = strconv.Itoa(int(sample>>ch) & 1)
					col++
				}
			}
			if err := c.Write(row); err != nil {
				return err
			}
		}
		c.Flush()
		return c.Error()
	case "vcd":
		return writeVCD(w, tr)
	default:
		return fmt.Errorf("logic: unknown logic output format %q; use json, csv, or vcd", format)
	}
}

// writeVCD writes a value change dump with one wire for each enabled input.
// Times are in picoseconds from the first sample.
func writeVCD(w io.Writer, tr *Trace) error {
	b := bufio.NewWriter(w)
	fmt.Fprintf(b, "$comment sample_rate_hz=%s trigger_sample=%d triggered=%t $end\n",
		strconv.FormatFloat(tr.Rate, 'g', -1, 64), tr.TriggerAt, tr.Triggered)
	fmt.Fprintln(b, "$timescale 1 ps $end")
	fmt.Fprintln(b, "$scope module logic $end")
	var enabled []int
	for ch := range 16 {
		if tr.Channels&(1<<ch) != 0 {
			fmt.Fprintf(b, "$var wire 1 %c L%d $end\n", '!'+ch, ch)
			enabled = append(enabled, ch)
		}
	}
	fmt.Fprintln(b, "$upscope $end")
	fmt.Fprintln(b, "$enddefinitions $end")
	ps := func(i int) int64 { return int64(math.Round(float64(i) * 1e12 / tr.Rate)) }
	prev := tr.At(0)
	fmt.Fprintln(b, "#0")
	fmt.Fprintln(b, "$dumpvars")
	for _, ch := range enabled {
		fmt.Fprintf(b, "%d%c\n", prev>>ch&1, '!'+ch)
	}
	fmt.Fprintln(b, "$end")
	for i := 1; i < tr.Len(); i++ {
		s := tr.At(i)
		if d := (s ^ prev) & tr.Channels; d != 0 {
			fmt.Fprintf(b, "#%d\n", ps(i))
			for _, ch := range enabled {
				if d>>ch&1 != 0 {
					fmt.Fprintf(b, "%d%c\n", s>>ch&1, '!'+ch)
				}
			}
		}
		prev = s
	}
	fmt.Fprintf(b, "#%d\n", ps(tr.Len()))
	return b.Flush()
}
