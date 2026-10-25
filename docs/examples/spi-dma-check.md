# SPI DMA check

Check for an extra SPI0 burst after a DMA transfer on SPI1, on a
Raspberry Pi Pico running a 1 MHz SPI test.

Connect GPIO18 (SPI0 SCK) to L0 and GPIO10 (SPI1 SCK) to L1. Connect the
grounds. Start the capture before the test sends its data:

```sh
logic-capture -device bitscope -rate 4000000 -samples 12000 \
  -channels 3 -trigger 1 -pre 0.05 -wait 10s \
  -format csv -output spi.csv
```

The capture triggers on L1, not L0. It holds 3 ms: 150 us before the
trigger and 2.85 ms after it.

Look for a second 512-pulse burst on L0 near the L1 burst. DMA completion
can occur before the last SPI1 clock edge, so the second SPI0 burst can
overlap the end of SPI1. Do not count only the edges after the last L1
edge. With the fix, there is no second SPI0 burst.
