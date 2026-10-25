# logic

Command line tools for logic analysers. The goal is to debug and develop
microcontroller features: capture the pins of a board, then read the
result as text or open it in a waveform viewer.

Supported analysers:

| Device | `-device` | Inputs | Details |
|---|---|---|---|
| BitScope Micro (BS05) | `bitscope` | L0 to L5 | [docs/bitscope.md](docs/bitscope.md) |
| DreamSourceLab DSLogic (original) | `dslogic` | L0 to L15 | [docs/dslogic.md](docs/dslogic.md) |

| Command | Use |
|---|---|
| [`logic-capture`](docs/logic-capture.md) | Capture once and write JSON, CSV, or VCD |
| `logic-probe` | Check that an analyser responds, without a capture |

Every command needs `-device`. There is no default device.

## Install

```sh
go install github.com/davecheney/logic/cmd/...@latest
```

The DSLogic needs libusb (`brew install libusb` on macOS). On Linux, see
[docs/linux.md](docs/linux.md) for the udev rule and serial port group.

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
`Write` writes a `Trace` as JSON, CSV, or VCD.

## Tests

```sh
go test ./...
```

The BitScope hardware tests are in [docs/bitscope.md](docs/bitscope.md).

## License

BSD 3-Clause. See [LICENSE](LICENSE). Third-party notices, including the
DSLogic firmware, are in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
