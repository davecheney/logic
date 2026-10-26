# logic-capture

`logic-capture` captures once and writes the samples to a file or stdout.

```sh
logic-capture -device bitscope -rate 4000000 -samples 12000 \
  -channels 3 -trigger 1 -pre 0.05 -wait 10s \
  -format csv -output capture.csv
```

## Trigger and timing

`-channels` is a bit mask. Value 3 selects L0 and L1. `-trigger` is an
input number. That input must be enabled. Use `-falling` for a falling
edge. The command waits for an edge by default. Use `-auto 100ms` to
permit a capture without an edge. `-wait` is the total capture deadline.
`-wait 0` waits until interrupted. An auto capture has `triggered=false`.
Do not use it to prove that an expected signal was absent after a trigger.

The limits on rate, sample count, and pre-trigger depend on the analyser.
See the [bitscope](https://pkg.go.dev/github.com/davecheney/logic/bitscope), [dslogic](https://pkg.go.dev/github.com/davecheney/logic/dslogic), and
[fx2lafw](https://pkg.go.dev/github.com/davecheney/logic/fx2lafw), and
[ols](https://pkg.go.dev/github.com/davecheney/logic/ols) package documentation.

`-rate` is a frequency with a unit: `mHz`, `Hz`, `kHz`, `MHz`, or `GHz`.
The units are case sensitive. A number without a unit is in Hz, so
`-rate 4MHz`, `-rate 4000000`, and `-rate 4e6` are the same.

## Output

Use `-output -` to write data to stdout. Status and errors go to stderr.
An existing output file is not overwritten.

JSON is the default format. It contains `sample_rate`, `channels`,
`trigger_channel`, `falling`, `trigger_sample`, `triggered`, and `samples`.
The sample field is a base64 string. Decode it to get one byte per sample.
If `sample_bytes` is 2, each sample is a little-endian 16-bit value
instead. Bit n is the state of Ln. Disabled bits are zero. The sample rate
in the file is the actual rate, not the requested rate. `sample_rate`
is a string with a unit, for example `"4MHz"` or `"2.5Hz"`. The actual
rate is rounded to the nearest milliHertz. The maximum rounding error is
0.0005 Hz.

Old JSON captures with a numeric `sample_rate_hz` field are not supported.
Use the same version of the capture and decoder commands.

CSV metadata is in lines that start with `#`, then a `time_seconds`
column and one column per enabled input. Its `sample_rate_hz` metadata
is still a number in Hertz. Times start at the first sample. Subtract
`trigger_sample / sample_rate_hz` to get time relative to the trigger.
With `triggered=false`, the trigger position is not a detected edge.

VCD has one wire per enabled input, named Ln, and holds only the changes,
so a long idle capture stays small. Times are in picoseconds from the
first sample. A `$comment` line holds the sample rate and the trigger
position. PulseView and GTKWave can open it.
