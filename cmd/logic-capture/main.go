// Command logic-capture saves one digital capture from a logic analyser.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"math/bits"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/analyzer"
	"github.com/davecheney/logic/dslogic"
)

type captureOptions struct {
	ChannelMask uint
	Rate        logic.Frequency
	Samples     int
	Source      int
	Falling     bool
	PreTrigger  float64
	Auto        time.Duration
}

func run(args []string, stdout, stderr io.Writer) (err error) {
	flags := flag.NewFlagSet("logic-capture", flag.ContinueOnError)
	flags.SetOutput(stderr)
	device := flags.String("device", "", "analyser, required: "+strings.Join(analyzer.Devices(), ", "))
	port := flags.String("port", "", "BitScope or OLS serial port (default: search)")
	threshold := flags.String("threshold", "3.3", "DSLogic input threshold: 3.3 or 5 (volts)")
	rate := 4 * logic.MegaHertz
	flags.Func("rate", "sample rate, such as 4MHz or 4000000 (Hz); supported rates depend on the analyser (default 4MHz)", func(s string) (err error) {
		rate, err = logic.ParseFrequency(s)
		return err
	})
	samples := flags.Int("samples", 12000, "number of samples; limits depend on the analyser and selected inputs")
	channels := flags.Uint("channels", 3, "digital input bit mask; bit n selects input n")
	source := flags.Int("trigger", 1, "trigger input number")
	falling := flags.Bool("falling", false, "trigger on a falling edge")
	pre := flags.Float64("pre", 0.05, "fraction of samples before the trigger")
	auto := flags.Duration("auto", 0, "auto trigger wait (0 waits for an edge)")
	wait := flags.Duration("wait", 10*time.Second, "overall capture deadline (0 waits until interrupted)")
	format := flags.String("format", "json", "output format: json, csv, or vcd")
	output := flags.String("output", "-", "output path, or - for stdout")
	preset := flags.String("preset", "", "long: DSLogic L0 to L3 at 1 MS/s for up to 60 s, set flags override it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	cfg := captureOptions{
		ChannelMask: *channels, Rate: rate, Samples: *samples,
		Source: *source, Falling: *falling, PreTrigger: *pre, Auto: *auto,
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if err := applyPreset(*preset, set, *device, &cfg, wait); err != nil {
		return err
	}
	var th dslogic.Threshold
	switch *threshold {
	case "3.3":
		th = dslogic.Threshold3V3
	case "5":
		th = dslogic.Threshold5V
	default:
		return errors.New("threshold must be 3.3 or 5")
	}
	caps, err := analyzer.CapabilitiesFor(*device)
	if err != nil {
		return err
	}
	req, err := captureRequest(&caps, cfg)
	if err != nil {
		return err
	}
	if err := analyzer.Validate(*device, req); err != nil {
		return err
	}
	if *format != "csv" && *format != "json" && *format != "vcd" {
		return errors.New("format must be json, csv, or vcd")
	}
	if *wait < 0 {
		return errors.New("wait must not be negative")
	}
	if *output != "-" {
		if _, err := os.Lstat(*output); err == nil {
			return fmt.Errorf("output %s already exists", *output)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	dev, err := analyzer.Open(*device, *port, th)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dev.Close()) }()
	caps = dev.Capabilities()
	req, err = captureRequest(&caps, cfg)
	if err != nil {
		return err
	}
	if err := dev.ValidateCapture(req); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *wait)
		defer cancel()
	}
	fmt.Fprintf(stderr, "%s: starting capture, trigger L%d; output %s\n", dev, cfg.Source, *output)
	tr, err := dev.CaptureLogic(ctx, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "%d samples at %.9g S/s, trigger sample %d, triggered=%t\n",
		tr.Len(), tr.Rate.Hz(), tr.TriggerAt, tr.Triggered)
	if *output != "-" {
		f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, f.Close()) }()
		stdout = f
	}
	return logic.Write(stdout, *format, tr)
}

type resourceCapabilities interface {
	Resources() iter.Seq[logic.Resource]
}

func captureRequest(caps resourceCapabilities, options captureOptions) (logic.CaptureRequest, error) {
	req := logic.CaptureRequest{
		Rate: options.Rate, Samples: options.Samples, Falling: options.Falling,
		PreTrigger: options.PreTrigger, Auto: options.Auto,
	}
	selectedInputs := make(map[uint]logic.ResourceID)
	otherResources := make(map[uint]logic.Resource)
	triggerFound := false
	for r := range caps.Resources() {
		index := r.ID.Index()
		if index >= uint(bits.UintSize) {
			continue
		}
		bit := uint(1) << index
		if r.Kind.Has(logic.KindDigitalIn) {
			if options.ChannelMask&bit != 0 {
				selectedInputs[index] = r.ID
			}
			if int(index) == options.Source {
				req.Trigger = r.ID
				triggerFound = true
			}
		} else if options.ChannelMask&bit != 0 || int(index) == options.Source {
			otherResources[index] = r
		}
	}
	for index := uint(0); index < uint(bits.UintSize); index++ {
		if options.ChannelMask&(uint(1)<<index) == 0 {
			continue
		}
		if _, ok := selectedInputs[index]; !ok {
			if other, exists := otherResources[index]; exists {
				return logic.CaptureRequest{}, fmt.Errorf("requested resource %q is not a digital input", other.Name)
			}
			return logic.CaptureRequest{}, fmt.Errorf("digital input L%d is not available", index)
		}
	}
	if options.Source < 0 || options.Source >= bits.UintSize {
		return logic.CaptureRequest{}, fmt.Errorf("trigger input %d is out of range", options.Source)
	}
	if !triggerFound {
		if other, ok := otherResources[uint(options.Source)]; ok {
			return logic.CaptureRequest{}, fmt.Errorf("requested resource %q is not a digital input", other.Name)
		}
		return logic.CaptureRequest{}, fmt.Errorf("trigger input L%d is not available", options.Source)
	}
	req.Inputs = slices.Collect(filterMap(
		caps.Resources(),
		func(r logic.Resource) bool {
			selected, ok := selectedInputs[r.ID.Index()]
			return ok && selected == r.ID && r.Kind.Has(logic.KindDigitalIn)
		},
		func(r logic.Resource) logic.ResourceID { return r.ID },
	))
	if len(req.Inputs) == 0 {
		return logic.CaptureRequest{}, errors.New("at least one input must be selected")
	}
	return req, nil
}

func filterMap[T, U any](seq iter.Seq[T], keep func(T) bool, mapValue func(T) U) iter.Seq[U] {
	return func(yield func(U) bool) {
		for value := range seq {
			if keep(value) && !yield(mapValue(value)) {
				return
			}
		}
	}
}

// applyPreset sets the options of a preset that are not in set, the flags
// given on the command line.
func applyPreset(name string, set map[string]bool, device string, cfg *captureOptions, wait *time.Duration) error {
	switch name {
	case "":
		return nil
	case "long":
	default:
		return errors.New("preset must be long")
	}
	if device != "dslogic" {
		return errors.New("the long preset needs -device dslogic")
	}
	if !set["channels"] {
		cfg.ChannelMask = 0x000f
	}
	if !set["rate"] {
		cfg.Rate = logic.MegaHertz
	}
	if !set["samples"] {
		caps, err := analyzer.CapabilitiesFor(device)
		if err != nil {
			return err
		}
		maxSamples, ok := caps.MaxSamplesFor(bits.OnesCount(cfg.ChannelMask))
		if !ok {
			return fmt.Errorf("%s: no sample limit for %d inputs", device, bits.OnesCount(cfg.ChannelMask))
		}
		cfg.Samples = min(int(60*cfg.Rate/logic.Hertz), maxSamples)
	}
	if !set["wait"] {
		capture := time.Duration(float64(cfg.Samples) / cfg.Rate.Hz() * float64(time.Second))
		*wait = 2*capture + 30*time.Second
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "logic-capture:", err)
		os.Exit(1)
	}
}
