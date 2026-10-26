# logic

Command line tools for logic analysers. The goal is to debug and develop
microcontroller features: capture the pins of a board, then read the
result as text or open it in a waveform viewer.

Supported analysers:

| Device | `-device` | Inputs | Details |
|---|---|---|---|
| BitScope Micro (BS05) | `bitscope` | L0 to L5 | [docs/bitscope.md](docs/bitscope.md) |
| DreamSourceLab DSLogic (original) | `dslogic` | L0 to L15 | [docs/dslogic.md](docs/dslogic.md) |
| Generic Saleae Logic clone (8 channels, 24 MHz) | `fx2lafw` or `saleae` | L0 to L7 | [docs/fx2lafw.md](docs/fx2lafw.md) |

| Command | Use |
|---|---|
| [`logic-capture`](docs/logic-capture.md) | Capture once and write JSON, CSV, or VCD |
| `logic-probe` | Check that an analyser responds, without a capture |
| [`logic-edges`](docs/logic-edges.md) | List the level changes in a capture |
| [`logic-i2c`](docs/logic-i2c.md) | Decode I2C in a capture |
| [`logic-uart`](docs/logic-uart.md) | Decode UART in a capture |

Every command that opens an analyser needs `-device`. There is no default
device. `logic-edges`, `logic-i2c`, and `logic-uart` read a `logic-capture`
JSON file.

## Install

```sh
go install github.com/davecheney/logic/cmd/...@latest
```

The DSLogic and the Saleae Logic clone need libusb (`brew install libusb`
on macOS). On Linux, see [docs/linux.md](docs/linux.md) for the udev rules
and serial port group.

## Example

```sh
logic-probe -device dslogic
logic-capture -device dslogic -rate 10000000 -samples 65536 \
  -channels 0xf -trigger 0 -auto 100ms -format vcd -output capture.vcd
```

More examples are in [docs/examples](docs/examples).

## Library

Package `logic` holds the drivers and the capture types. `Open` opens an
analyser by name, `Validate` checks a `Config` without opening one, and
`Write` writes a `Trace` as JSON, CSV, or VCD. `Read` reads the JSON back.

`Config.Rate` and `Trace.Rate` use `Frequency`, stored in milliHertz.
For example, use `4 * logic.MegaHertz` for 4 MHz, or `rate.Hz()` when a
calculation needs Hertz as a `float64`. Actual digital trace rates are
rounded to the nearest milliHertz.

Trace JSON uses a `sample_rate` string, such as `"4MHz"`. `Frequency`
implements JSON encoding and decoding with the same unit notation as
`String` and `ParseFrequency`. Old captures with a numeric
`sample_rate_hz` field are not supported.

## Tests

```sh
go test ./...
```

The BitScope hardware tests are in [docs/bitscope.md](docs/bitscope.md).

## License

BSD 3-Clause. See [LICENSE](LICENSE). Third-party notices, including the
DSLogic firmware, are in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
