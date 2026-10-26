// Command logic-capture saves one digital capture from a logic analyser.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/bits"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/davecheney/logic"
)

func run(args []string, stdout, stderr io.Writer) (err error) {
	flags := flag.NewFlagSet("logic-capture", flag.ContinueOnError)
	flags.SetOutput(stderr)
	device := flags.String("device", "", "analyser, required: "+strings.Join(logic.Devices(), ", "))
	port := flags.String("port", "", "BitScope serial port (default: search)")
	threshold := flags.String("threshold", "3.3", "DSLogic input threshold: 3.3 or 5 (volts)")
	rate := 4 * logic.MegaHertz
	flags.Func("rate", "sample rate, such as 4MHz or 4000000 (Hz): BitScope at most 8MHz, DSLogic at most 400MHz, FX2 at most 24MHz (default 4MHz)", func(s string) (err error) {
		rate, err = logic.ParseFrequency(s)
		return err
	})
	samples := flags.Int("samples", 12000, "sample count: BitScope at most 12288")
	channels := flags.Uint("channels", 3, "input bit mask: bit n is Ln; BitScope L0 to L5, DSLogic L0 to L15, FX2 L0 to L7")
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
	if *channels > 0xffff {
		return errors.New("channels must be a 16-bit mask")
	}
	cfg := logic.Config{
		Channels: uint16(*channels), Rate: rate, Samples: *samples,
		Source: *source, Falling: *falling, PreTrigger: *pre, Auto: *auto,
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if err := applyPreset(*preset, set, *device, &cfg, wait); err != nil {
		return err
	}
	var th logic.Threshold
	switch *threshold {
	case "3.3":
		th = logic.Threshold3V3
	case "5":
		th = logic.Threshold5V
	default:
		return errors.New("threshold must be 3.3 or 5")
	}
	if err := logic.Validate(*device, cfg); err != nil {
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
	dev, err := logic.Open(*device, *port, th)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, dev.Close()) }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *wait)
		defer cancel()
	}
	fmt.Fprintf(stderr, "%s: starting capture, trigger L%d; output %s\n", dev, cfg.Source, *output)
	tr, err := dev.CaptureLogic(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "%d samples at %.9g S/s, trigger sample %d, triggered=%t\n",
		tr.Len(), tr.Rate, tr.TriggerAt, tr.Triggered)
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

// applyPreset sets the options of a preset that are not in set, the flags
// given on the command line.
func applyPreset(name string, set map[string]bool, device string, cfg *logic.Config, wait *time.Duration) error {
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
		cfg.Channels = 0x000f
	}
	if !set["rate"] {
		cfg.Rate = logic.MegaHertz
	}
	if !set["samples"] {
		cfg.Samples = min(int(60*cfg.Rate/logic.Hertz), logic.DSLogicMaxSamples(bits.OnesCount16(cfg.Channels)))
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
