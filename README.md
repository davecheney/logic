# logic

Command line tools for logic analysers. The goal is to debug and develop
microcontroller features: capture the pins of a board, then read the
result as text or open it in a waveform viewer.

Supported analysers:

| Device | `-device` | Inputs | Details |
|---|---|---|---|
| BitScope Micro (BS05) | `bitscope` | L0 to L5 | [docs/bitscope.md](docs/bitscope.md) |
| DreamSourceLab DSLogic (original) | `dslogic` | L0 to L15 | [docs/dslogic.md](docs/dslogic.md) |
| Open Workbench Logic Sniffer | `ols` | L0 to L15 | [docs/ols.md](docs/ols.md) |
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
on macOS). The OLS and BitScope need access to the serial port. On Linux,
see [docs/linux.md](docs/linux.md) for the udev rules and serial port group.

## Example

```sh
logic-probe -device ols
logic-capture -device ols -rate 10MHz -samples 12000 \
  -channels 0xf -trigger 0 -pre 0.05 -format vcd -output capture.vcd
```

More examples are in [docs/examples](docs/examples).

## Library

Package `logic` holds the drivers and capture types. `Open` opens an
analyser by name. `CapabilitiesFor` reports a device's resources without
opening it. `ValidateCapture` checks a request on an open analyser.
`Write` writes a `Trace` as JSON, CSV, or VCD. `Read` reads JSON captures.

`CaptureRequest.Rate` and `Trace.Rate` use `Frequency`, stored in milliHertz.
For example, use `4 * logic.MegaHertz` for 4 MHz, or `rate.Hz()` when a
calculation needs Hertz as a `float64`. Actual digital trace rates are
rounded to the nearest milliHertz.

Trace JSON uses a `sample_rate` string, such as `"4MHz"`. `Frequency`
implements JSON encoding and decoding with the same unit notation as
`String` and `ParseFrequency`. Old captures with a numeric
`sample_rate_hz` field are not supported.

`Analyzer.Capabilities` returns a `CapabilitySet`. It describes the
resources, their roles, rate limits, suggested rates, and any sample
limits. Use `for resource := range caps.Resources()` to read its resources
and `for limit := range caps.SampleLimits()` to read its sample limits.
Use `for relation := range caps.Relations()` to read resource relations.
A sample limit gives the maximum sample count for a number of enabled
inputs. Each driver has a device class and a unique instance ID.
Resource and pin IDs refer to one device instance. A `PhysicalPin` uses
the device class, instance, and pin number. Pin 0 on a BitScope is not
pin 0 on a Saleae clone or on another BitScope.

Relations show when resources share a pin, such as BitScope L4 and the
AWG output. Some settings limit each other. For example, the DSLogic
supports fewer inputs at 200 and 400 MHz. `ValidateCapture` reports an
error when a request uses an invalid setting.

## Tests

```sh
go test ./...
```

The BitScope hardware tests are in [docs/bitscope.md](docs/bitscope.md).

## License

BSD 3-Clause. See [LICENSE](LICENSE). Third-party notices, including the
DSLogic firmware, are in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
