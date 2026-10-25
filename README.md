# logic

Command line tools for logic analysers. The goal is to debug and develop
microcontroller features: capture the pins of a board, then read the
result as text or open it in a waveform viewer.

Supported analysers:

- BitScope Micro (BS05), inputs L0 through L5
- Original DreamSourceLab DSLogic (USB ID 2A0E:0001, 2014 Kickstarter),
  inputs L0 through L15

| Command | Use |
|---|---|
| `logic-capture` | Capture once and write JSON, CSV, or VCD |
| `logic-probe` | Check that an analyser responds, without a capture |

Every command needs `-device`: `bitscope` or `dslogic`. There is no
default device.

## Install

```sh
go install github.com/davecheney/logic/cmd/...@latest
```

The DSLogic needs libusb. On macOS, run `brew install libusb`. On Linux,
install libusb (`libusb-1.0-0-dev` on Debian and Ubuntu, `libusb` on Arch).

### Linux permissions

By default only root can open the DSLogic. Add a udev rule for its USB
vendor ID, 2A0E:

```sh
echo 'SUBSYSTEM=="usb", ATTRS{idVendor}=="2a0e", MODE="0666"' | \
  sudo tee /etc/udev/rules.d/60-dslogic.rules
sudo udevadm control --reload
sudo udevadm trigger
```

Then unplug the DSLogic and plug it in again. The rule also covers the
device after the tools load its firmware, because the USB ID stays the
same. Without the rule, the tools fail with `libusb: bad access`.

The BitScope is a USB serial port. Add your user to the group that owns
serial ports, `dialout` on Debian and Ubuntu or `uucp` on Arch, then log
in again:

```sh
sudo usermod -aG dialout $USER
```

## logic-capture

```sh
logic-capture -device bitscope -rate 4000000 -samples 12000 \
  -channels 3 -trigger 1 -pre 0.05 -wait 10s \
  -format csv -output spi.csv
```

`-port` selects the BitScope serial port. Without it, the tool searches
for one.

`-channels` is a bit mask. Value 3 selects L0 and L1. `-trigger` is an
input number. That input must be enabled. Use `-falling` for a falling
edge. The command waits for an edge by default. Use `-auto 100ms` to
permit a capture without an edge. `-wait` is the total capture deadline.
`-wait 0` waits until interrupted. An auto capture has `triggered=false`.
Do not use it to prove that an expected signal was absent after a trigger.

JSON is the default format. It contains `sample_rate_hz`, `channels`,
`trigger_channel`, `falling`, `trigger_sample`, `triggered`, and `samples`.
The sample field is a base64 string. Decode it to get one byte per sample.
If `sample_bytes` is 2, each sample is a little-endian 16-bit value
instead. Bit n is the state of Ln. Disabled bits are zero. The sample rate
in the file is the actual rate, not the requested rate.

CSV has the same metadata in lines that start with `#`, then a
`time_seconds` column and one column per enabled input. Times start at
the first sample. Subtract `trigger_sample / sample_rate_hz` to get time
relative to the trigger. With `triggered=false`, the trigger position is
not a detected edge.

VCD has one wire per enabled input, named Ln, and holds only the changes,
so a long idle capture stays small. Times are in picoseconds from the
first sample. A `$comment` line holds the sample rate and the trigger
position. PulseView and GTKWave can open it.

Use `-output -` to write data to stdout. Status and errors go to stderr.
An existing output file is not overwritten.

## BitScope BS05

Use the six direct inputs, L0 through L5. The BS05 can also use the two
analog comparators as logic inputs. These tools do not enable those
comparator inputs.

Close other BitScope programs before you capture. Connect the signal
ground to a BitScope ground pin. Use known 3.3 V signals for the initial
check. Check the hardware manual for input ratings before you connect
other signals. L4 shares the AWG pin. L5 shares the clock output pin.
Keep both outputs off when you connect external signals. The logic tools
do not enable either output.

One byte holds all selected inputs. The 12 KiB buffer holds at most 12288
samples, whether one or six inputs are selected. The tools use normal
logic mode, from about 2442 S/s through 8 MS/s. Faster hardware modes are
not enabled. The actual rate is rounded down to a device clock step.
Pre-trigger capture requires a rate of at most 5 MS/s. The capture and
auto timeout must fit within the 419 ms hardware timer.

Digital capture has been checked on BS000500 with a 1 MHz SPI signal.
At 4 MS/s, clock periods within each byte span four samples. The L1
rising-edge trigger appeared at the requested sample 600. L0 captured
512 clock pulses. The corresponding L2 data decoded to bytes 0x40 through
0x7F in an external check.

### SPI DMA check

For the 1 MHz Pico SPI test, connect GPIO18 (SPI0 SCK) to L0 and GPIO10
(SPI1 SCK) to L1. Connect the grounds. The command above triggers on L1,
not L0. Its 3 ms capture has 150 us of pre-trigger data and 2.85 ms after
the trigger. Start capture before the test sends its data.

Look for a second 512-pulse burst on L0 near the L1 burst. DMA completion
can occur before the last SPI1 clock edge, so the second SPI0 burst can
overlap the end of SPI1. Do not count only the edges after the last L1
edge. With the fix, there is no second SPI0 burst.

### Probe

For a BitScope, `logic-probe` sends only VM reset (`!`) and identify (`?`).

```sh
logic-probe -device bitscope
```

The connected device returned `!\x3f\rBS000500\r` across the two commands.
It replied at both 460800 and 115200 baud. The FT245R uses a parallel FIFO,
not a UART, so these results do not select a UART baud rate.

## DSLogic

```sh
logic-capture -device dslogic -rate 10000000 -samples 65536 \
  -channels 0xffff -trigger 0 -auto 100ms -format csv -output dsl.csv
```

On open, the tools load the FX2 firmware and the FPGA bitstream. The
device then connects again with the same USB ID. The green LED comes on
when the FPGA is ready. Close DSView before you use these tools.

The original DSLogic has a 22-pin header. From left to right, as in the
[2015 DreamSourceLab tech specs](https://web.archive.org/web/20150317030251/http://dreamsourcelab.com/techspec.html):

```
5.0V TO TI CLK | GND CH15 ... CH8 | GND CH7 ... CH0
```

CH0 is the pin at the right end. 5.0V is a supply output. Do not connect
it to a signal. CLK, TI, and TO are 3.3 V only. The tools use only CH0
through CH15.

The oscilloscope extension plugs into this header. Its
[schematic](https://web.archive.org/web/20171005205514/http://dreamsourcelab.com/download/Schematic_of_DSLogic_oscilloscope_extension_v1_0.pdf)
(J1) uses the pins as follows:

| DSLogic | Extension | Use |
|---|---|---|
| 5.0V | VIN_5.0V | power |
| TO | CTL1 | control |
| TI | CTL0 | control |
| CLK | CLK_IN | ADC clock, an output from the DSLogic |
| CH15 to CH8 | DA7 to DA0 | ADC A data |
| CH7 to CH0 | DB7 to DB0 | ADC B data |

The tools do not support the extension.

`-threshold` selects the input threshold: `3.3` (default) or `5` volts.
The threshold sets which FPGA bitstream the tools load.

The DSLogic has 256 Mbit of sample memory, shared by the enabled inputs.
16 inputs can hold 16777216 samples each. The rate is 100 MS/s divided by
a whole number, from 50 kS/s through 100 MS/s. At 200 MS/s, use only L0
through L7. At 400 MS/s, use only L0 through L3. The tools round the
sample count up to a multiple of 1024 for the device, then return the
requested count.

In Auto mode, the DSLogic first waits for the trigger edge. If no edge
occurs, it captures again without a trigger. Such a capture has
`triggered=false`.

For a long capture, use `-preset long`:

```sh
logic-capture -device dslogic -preset long -trigger 3 -format vcd -output long.vcd
```

The preset needs the DSLogic. It selects L0 through L3 and 1 MS/s, and
captures for 60 s, or less if the enabled inputs cannot hold 60 s of
samples. The deadline is twice the capture time plus 30 s, which allows
for an Auto capture. A flag given on the command line overrides the
preset value. A 60 s capture with Auto and no edge takes about 121 s.

To check the device without a capture:

```sh
logic-probe -device dslogic
```

This loads the firmware and prints the FPGA status and HDL version.

The tools support only buffer mode with a simple edge trigger. Stream
mode, RLE, external clock, multi-stage triggers, and serial triggers are
not supported.

Checks on hardware: the firmware loads, the FPGA configures with both
bitstreams, and auto captures return the expected size with open inputs.

Signal checks used a XIAO ESP32-C3 at 1 MS/s and 10 MS/s with the 3.3 V
bitstream. The FPGA internal test pattern (mode bit 15) decodes as a
counter, with L0 changing on every sample. A 5 V input reads high on L7
only. Four outputs toggled at periods in the ratio 1:2:4:8 appear on the
expected inputs. A rising-edge trigger fires, and `trigger_sample` is the
first sample after the edge. 100 kHz I2C decodes correctly.

Falling-edge triggers, signals above 10 MS/s, and the 5 V bitstream with a
signal are not yet verified.

## Library

Package `logic` holds the drivers and the capture types. `Open` opens an
analyser by name, `Validate` checks a `Config` without opening one, and
`Write` writes a `Trace` as JSON, CSV, or VCD. The BitScope driver also
supports analog capture and the AWG.

## Tests

```sh
go test ./...
BITSCOPE_PORT=/dev/ttyUSB0 go test -v -count=1 -run '^TestDevice$' .
BITSCOPE_LOGIC_PORT=/dev/ttyUSB0 go test -v -count=1 -run '^TestLogicDevice$' .
```

The `BITSCOPE_PORT` hardware tests change capture settings but do not
program the output generator.

## References

- [BitScope VM](https://docs.bitscope.com/virtual-machine/)
- [BitLib guide](https://docs.bitscope.com/UZWFJ5CE/)
- [scopething](https://github.com/jonathanhogg/scopething): register map,
  capture modes, and nominal conversion values. See `THIRD_PARTY_NOTICES`.
- [DSView](https://github.com/DreamSourceLab/DSView): DSLogic USB
  protocol, firmware, and FPGA bitstreams. See `THIRD_PARTY_NOTICES`.
