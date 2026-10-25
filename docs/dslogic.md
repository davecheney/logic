# DSLogic

The original DreamSourceLab DSLogic, USB ID 2A0E:0001, from the 2014
Kickstarter. Use `-device dslogic`.

```sh
logic-capture -device dslogic -rate 10000000 -samples 65536 \
  -channels 0xffff -trigger 0 -auto 100ms -format csv -output dsl.csv
```

On open, the tools load the FX2 firmware and the FPGA bitstream. The
device then connects again with the same USB ID. The green LED comes on
when the FPGA is ready. Close DSView before you use these tools.

## Pinout

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

## Threshold

`-threshold` selects the input threshold: `3.3` (default) or `5` volts.
The threshold sets which FPGA bitstream the tools load.

## Memory and rates

The DSLogic has 256 Mbit of sample memory, shared by the enabled inputs.
16 inputs can hold 16777216 samples each. The rate is 100 MS/s divided by
a whole number, from 50 kS/s through 100 MS/s. At 200 MS/s, use only L0
through L7. At 400 MS/s, use only L0 through L3. The tools round the
sample count up to a multiple of 1024 for the device, then return the
requested count.

In Auto mode, the DSLogic first waits for the trigger edge. If no edge
occurs, it captures again without a trigger. Such a capture has
`triggered=false`.

## Long captures

```sh
logic-capture -device dslogic -preset long -trigger 3 -format vcd -output long.vcd
```

The preset needs the DSLogic. It selects L0 through L3 and 1 MS/s, and
captures for 60 s, or less if the enabled inputs cannot hold 60 s of
samples. The deadline is twice the capture time plus 30 s, which allows
for an Auto capture. A flag given on the command line overrides the
preset value. A 60 s capture with Auto and no edge takes about 121 s.

## Probe

```sh
logic-probe -device dslogic
```

This loads the firmware and prints the FPGA status and HDL version.

## Limits

The tools support only buffer mode with a simple edge trigger. Stream
mode, RLE, external clock, multi-stage triggers, and serial triggers are
not supported.

## Checks on hardware

The firmware loads, the FPGA configures with both bitstreams, and auto
captures return the expected size with open inputs.

Signal checks used a XIAO ESP32-C3 at 1 MS/s and 10 MS/s with the 3.3 V
bitstream. The FPGA internal test pattern (mode bit 15) decodes as a
counter, with L0 changing on every sample. A 5 V input reads high on L7
only. Four outputs toggled at periods in the ratio 1:2:4:8 appear on the
expected inputs. A rising-edge trigger fires, and `trigger_sample` is the
first sample after the edge. 100 kHz I2C decodes correctly.

Falling-edge triggers, signals above 10 MS/s, and the 5 V bitstream with a
signal are not yet verified.

## References

- [DSView](https://github.com/DreamSourceLab/DSView): DSLogic USB
  protocol, firmware, and FPGA bitstreams. See `THIRD_PARTY_NOTICES`.
