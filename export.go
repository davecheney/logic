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

// traceInput is one entry of the JSON inputs list. Device numbers are local
// to the file: equal numbers are the same device.
type traceInput struct {
	Device int    `json:"device"`
	Class  string `json:"class,omitempty"`
	Index  uint   `json:"index"`
}

// traceJSON is the JSON form of a Trace. Samples is base64. Channels is
// the input mask of files that have no inputs list; Write does not use it.
type traceJSON struct {
	Rate      Frequency     `json:"sample_rate"`
	Inputs    []*traceInput `json:"inputs,omitempty"`
	Channels  uint64        `json:"channels,omitempty"`
	Trigger   *int          `json:"trigger_channel,omitempty"`
	Falling   bool          `json:"falling"`
	AnyEdge   bool          `json:"any_edge,omitempty"`
	TriggerAt int           `json:"trigger_sample"`
	Triggered bool          `json:"triggered"`
	Width     int           `json:"sample_bytes,omitempty"`
	Samples   []byte        `json:"samples"`
}

// devices returns a number, from one, for each device in t, in the order
// of their first input.
func (t *Trace) devices() map[*Device]int {
	numbers := make(map[*Device]int)
	for _, id := range t.Inputs {
		if id != (ResourceID{}) && numbers[id.device] == 0 {
			numbers[id.device] = len(numbers) + 1
		}
	}
	return numbers
}

// inputNames returns a label for each input bit. Inputs on a single device
// are Ln; otherwise the label also has the device class and number.
func (t *Trace) inputNames() []string {
	numbers := t.devices()
	names := make([]string, len(t.Inputs))
	for bit, id := range t.Inputs {
		switch {
		case id == (ResourceID{}):
		case len(numbers) == 1:
			names[bit] = "L" + strconv.FormatUint(uint64(id.index), 10)
		default:
			names[bit] = fmt.Sprintf("%s%d.L%d", id.device.class, numbers[id.device], id.index)
		}
	}
	return names
}

func (t *Trace) MarshalJSON() ([]byte, error) {
	v := traceJSON{
		Rate: t.Rate, Falling: t.Falling, AnyEdge: t.AnyEdge, TriggerAt: t.TriggerAt,
		Triggered: t.Triggered, Width: t.Width, Samples: t.Samples,
	}
	numbers := t.devices()
	for _, id := range t.Inputs {
		var in *traceInput
		if id != (ResourceID{}) {
			in = &traceInput{Device: numbers[id.device], Index: id.index}
			if id.device.class != 0 {
				in.Class = id.device.class.String()
			}
		}
		v.Inputs = append(v.Inputs, in)
	}
	if bit, ok := t.Bit(t.Trigger); ok {
		v.Trigger = &bit
	}
	return json.Marshal(v)
}

func (t *Trace) UnmarshalJSON(data []byte) error {
	var v traceJSON
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*t = Trace{
		Rate: v.Rate, Falling: v.Falling, AnyEdge: v.AnyEdge, TriggerAt: v.TriggerAt,
		Triggered: v.Triggered, Width: v.Width, Samples: v.Samples,
	}
	if v.Inputs == nil {
		// Files from before the inputs list do not record the device class.
		t.Inputs = NewDevice(0).TraceInputs(v.Channels)
	}
	devices := make(map[int]*Device)
	for _, in := range v.Inputs {
		if in == nil {
			t.Inputs = append(t.Inputs, ResourceID{})
			continue
		}
		class, err := parseDeviceClass(in.Class)
		if err != nil {
			return err
		}
		d := devices[in.Device]
		if d == nil {
			d = NewDevice(class)
			devices[in.Device] = d
		} else if d.class != class {
			return fmt.Errorf("device %d has classes %q and %q", in.Device, d.class, class)
		}
		id := d.ResourceID(KindDigitalIn, in.Index)
		if _, ok := t.Bit(id); ok {
			return fmt.Errorf("input %s%d.L%d is listed more than once", in.Class, in.Device, in.Index)
		}
		t.Inputs = append(t.Inputs, id)
	}
	if v.Trigger != nil && *v.Trigger >= 0 && *v.Trigger < len(t.Inputs) {
		t.Trigger = t.Inputs[*v.Trigger]
	}
	return nil
}

// parseDeviceClass returns the class with the given name. The empty name
// is the unknown class, zero.
func parseDeviceClass(name string) (DeviceClass, error) {
	if name == "" {
		return 0, nil
	}
	for c := DeviceClass(1); int(c) < len(_DeviceClass_index); c++ {
		if c.String() == name {
			return c, nil
		}
	}
	return 0, fmt.Errorf("unknown device class %q", name)
}

// Read reads a JSON capture written by Write.
func Read(r io.Reader) (*Trace, error) {
	var tr Trace
	if err := json.NewDecoder(r).Decode(&tr); err != nil {
		return nil, fmt.Errorf("logic: read capture: %w", err)
	}
	switch {
	case tr.AnyEdge && tr.Falling:
		return nil, fmt.Errorf("logic: read capture: any-edge and falling-edge triggers are mutually exclusive")
	case tr.Width != 0 && tr.Width != 1 && tr.Width != 2 && tr.Width != 4 && tr.Width != 8:
		return nil, fmt.Errorf("logic: read capture: sample_bytes %d is not 1, 2, 4, or 8", tr.Width)
	case len(tr.Samples)%tr.width() != 0:
		return nil, fmt.Errorf("logic: read capture: %d bytes is not a whole number of %d-byte samples", len(tr.Samples), tr.width())
	case len(tr.Inputs) > 8*tr.width():
		return nil, fmt.Errorf("logic: read capture: %d inputs do not fit in %d-byte samples", len(tr.Inputs), tr.width())
	case tr.Rate == 0:
		return nil, fmt.Errorf("logic: read capture: invalid sample_rate")
	case tr.Len() == 0:
		return nil, fmt.Errorf("logic: read capture: no samples")
	}
	return &tr, nil
}

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
		trigger, ok := tr.Bit(tr.Trigger)
		if !ok {
			trigger = -1
		}
		c := csv.NewWriter(w)
		for _, metadata := range []string{
			fmt.Sprintf("# sample_rate_hz=%s", strconv.FormatFloat(tr.Rate.Hz(), 'g', -1, 64)),
			fmt.Sprintf("# channels=%d", tr.Mask()),
			fmt.Sprintf("# trigger_channel=%d", trigger),
			fmt.Sprintf("# falling=%t", tr.Falling),
			fmt.Sprintf("# trigger_sample=%d", tr.TriggerAt),
			fmt.Sprintf("# triggered=%t", tr.Triggered),
		} {
			if err := c.Write([]string{metadata}); err != nil {
				return err
			}
		}
		if tr.AnyEdge {
			if err := c.Write([]string{"# any_edge=true"}); err != nil {
				return err
			}
		}
		header := []string{"time_seconds"}
		var enabled []int
		for bit, name := range tr.inputNames() {
			if name != "" {
				header = append(header, name)
				enabled = append(enabled, bit)
			}
		}
		if err := c.Write(header); err != nil {
			return err
		}
		row := make([]string, len(header))
		for i := range tr.Len() {
			sample := tr.At(i)
			row[0] = strconv.FormatFloat(float64(i)/tr.Rate.Hz(), 'g', -1, 64)
			for col, bit := range enabled {
				row[col+1] = strconv.FormatUint(sample>>bit&1, 10)
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

// writeVCD writes a value change dump with one wire for each input.
// Times are in picoseconds from the first sample.
func writeVCD(w io.Writer, tr *Trace) error {
	b := bufio.NewWriter(w)
	fmt.Fprintf(b, "$comment sample_rate_hz=%s trigger_sample=%d triggered=%t $end\n",
		strconv.FormatFloat(tr.Rate.Hz(), 'g', -1, 64), tr.TriggerAt, tr.Triggered)
	if tr.AnyEdge {
		fmt.Fprintln(b, "$comment any_edge=true $end")
	}
	fmt.Fprintln(b, "$timescale 1 ps $end")
	fmt.Fprintln(b, "$scope module logic $end")
	var enabled []int
	for bit, name := range tr.inputNames() {
		if name != "" {
			fmt.Fprintf(b, "$var wire 1 %c %s $end\n", '!'+bit, name)
			enabled = append(enabled, bit)
		}
	}
	fmt.Fprintln(b, "$upscope $end")
	fmt.Fprintln(b, "$enddefinitions $end")
	ps := func(i int) int64 { return int64(math.Round(float64(i) * 1e12 / tr.Rate.Hz())) }
	mask := tr.Mask()
	prev := tr.At(0)
	fmt.Fprintln(b, "#0")
	fmt.Fprintln(b, "$dumpvars")
	for _, bit := range enabled {
		fmt.Fprintf(b, "%d%c\n", prev>>bit&1, '!'+bit)
	}
	fmt.Fprintln(b, "$end")
	for i := 1; i < tr.Len(); i++ {
		s := tr.At(i)
		if d := (s ^ prev) & mask; d != 0 {
			fmt.Fprintf(b, "#%d\n", ps(i))
			for _, bit := range enabled {
				if d>>bit&1 != 0 {
					fmt.Fprintf(b, "%d%c\n", s>>bit&1, '!'+bit)
				}
			}
		}
		prev = s
	}
	fmt.Fprintf(b, "#%d\n", ps(tr.Len()))
	return b.Flush()
}
