# Open Workbench Logic Sniffer

Use `-device ols` for an Open Workbench Logic Sniffer with Demon Core
firmware. The device has USB ID `04d8:fc92` and appears as a USB serial
port. Close other capture programs before you use it.

```sh
logic-probe -device ols
```

The tool finds one matching device. If more than one is connected, select
the port with `-port`. You can also use `-port` when automatic discovery
does not find the device.

## Inputs and limits

The driver supports the 16 buffered inputs, L0 to L15. It does not support
the 16 expansion inputs. Connect the analyser ground to the target ground.
Check the electrical limits for your board before you connect an input.

The driver uses the internal 100 MHz clock. It supports sample rates from
about 5961 samples per second to 100 million samples per second. The
device reports a 200 MHz maximum rate, but this needs demux mode. The
driver does not use demux mode.

The sample memory is 24,576 bytes. The device stores one byte for each
enabled group of eight inputs. One active group supports up to 24,576
samples. Two active groups support up to 12,288 samples. The requested
sample count is rounded up to a multiple of four by the device, then the
driver returns the requested number of samples.

The driver does not use run-length encoding, an external clock, or the
unbuffered expansion inputs. It does not configure the FPGA or PIC
firmware.

## Capture

```sh
logic-capture -device ols -rate 10MHz -samples 12000 \
  -channels 0xffff -trigger 0 -falling -pre 0.05 \
  -format vcd -output capture.vcd
```

`-channels` is a bit mask. Bit n selects input Ln. The trigger input must
also be selected. The driver supports rising and falling edge triggers. It
uses two trigger stages to detect each edge.

The requested rate can be any value in the supported range. The driver
uses a whole-number clock divider. The actual rate is rounded down to a
supported clock step and is stored in the capture file.

`-pre` sets the fraction of samples before the trigger. The trigger
position includes the two trigger stages. If `-auto` is set, the driver
ends an untriggered capture after the requested capture time and the auto
timeout. Its trace has `triggered=false`. `-wait` is the overall capture
deadline.

The driver does not support 200 MHz demux, RLE compression, an external
clock, or AWG output.
