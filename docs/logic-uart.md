# logic-uart

`logic-uart` decodes UART traffic on one input of a `logic-capture` JSON
file. It prints one line for each run of text.

```sh
logic-capture -device dslogic -rate 10000000 -samples 200000 \
  -channels 0x2 -trigger 1 -falling -output uart.json
logic-uart -input uart.json -line 1 -baud 115200
```

Capture at 4 or more samples per bit. 10 or more is better. At 115200
baud, 2 MS/s is enough. `-line` is required.

## Output

```
-0.005569300 "UART0 before\r\n"
+0.000008200 "\x00" framing=0
+0.001273000 "UART0 before\r\n"
```

The first column is the time of the first start bit in seconds relative
to the trigger. The text is a Go quoted string, so control bytes show as
escapes. A line ends after a newline, or when the line is idle for longer
than `-gap`. The default gap is 10 frame times.

`framing=` lists the bytes in the line whose stop bit was low. A line held
low, such as during a reset, shows as `"\x00"` with a framing error.
`parity=` lists the bytes with a parity error.

## Options

| Option | Default | Use |
|---|---|---|
| `-line` | | input number, required |
| `-baud` | 115200 | bits per second |
| `-parity` | none | none, even, or odd |
| `-stop` | 1 | stop bits, 1 or 2 |
| `-gap` | 10 frames | idle time that ends a line |
| `-hex` | false | print bytes in hex, and do not end lines at a newline |

Frames are 8 data bits, LSB first, with the line idle high.
