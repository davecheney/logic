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

`Analyzer.Capabilities` returns a `CapabilitySet`: the inputs and
outputs of an open analyser, with their roles, rate limits, and a short
list of suggested rates. Each driver holds a device object with its class and an instance ID that is unique in the process. Capability builders use that object to create resource and pin identities. `ResourceID` and `PhysicalPin` refer to the device object; use their `Device` method to read it, and `Device().Class()` for its class.
A `PhysicalPin` is device class, device instance, and pin, so pin 0 on a BitScope is not pin 0 on a Saleae clone, or pin 0 on a second BitScope.
Typed relations show pins that two resources share, such as BitScope L4
and the AWG output. Some settings limit each other, for example the
DSLogic channels at 200 and 400 MHz. The set does not show these limits;
`ValidateLogic` returns an error for them.

## Tests

```sh
go test ./...
```

The BitScope hardware tests are in [docs/bitscope.md](docs/bitscope.md).

## License

BSD 3-Clause. See [LICENSE](LICENSE). Third-party notices, including the
DSLogic firmware, are in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
