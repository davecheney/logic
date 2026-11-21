# logic-spi

`logic-spi` decodes SPI traffic in a `logic-capture` JSON file, one line
per frame.

```sh
logic-capture -device dslogic -rate 400MHz -samples 1000000 \
  -channels 0xf -trigger 3 -falling -output spi.json
logic-spi -input spi.json -sck 0 -sdo 1 -sdi 2 -cs 3 -mode 0
```

With 4 or fewer inputs, the DSLogic samples at 400 MS/s. Capture at 10
or more samples per SCK period to measure the SCK frequency. 4 samples a
period are enough to decode the data. `-sck` and one of `-sdo` and `-sdi`
are required.

## Frames

With `-cs`, a frame starts when CS goes low and ends when CS goes high.
SCK edges while CS is high are ignored. Without `-cs`, a frame ends when
SCK is idle for longer than `-gap`. The default gap is 10 SCK periods.

## Output

```
+0.000001250 bits=16 sck=10.000MHz SDO 9f 00 SDI ff ef
+0.000004000 bits=24 sck=9.998MHz SDO 12 34 56 SDI 00 00 00 gap=1.2µs
```

The first column is the time of the start of the frame in seconds
relative to the trigger. Then:

| Token | Meaning |
|---|---|
| `bits=` | number of SCK edges that latch data |
| `sck=` | mean SCK frequency, from the periods within 25% of the median |
| `SDO`, `SDI` | the bytes on each line, in hex |
| `?` | a byte with fewer than 8 bits at the end of the frame |
| `gap=` | the longest period between two latch edges, when it is more than 1.5 times the median |
| `unstable=` | number of bits where the data line changed on the latch edge |
| `idle=` | SCK level when CS went low, when it is not the level for `-mode` |

`unstable=` usually means that `-mode` is not the mode on the bus, or
that the capture rate is too low. `gap=` shows a pause in a frame, for
example between two FIFO loads.

## Options

| Option | Default | Use |
|---|---|---|
| `-sck` | | SCK input number, required |
| `-sdo` | | SDO (MOSI) input number |
| `-sdi` | | SDI (MISO) input number |
| `-cs` | | CS input number, active low |
| `-mode` | 0 | SPI mode, 0 to 3 |
| `-lsb` | false | bytes are LSB first |
| `-gap` | 10 SCK periods | without `-cs`, idle time that ends a frame |

Modes 0 and 3 latch data on the rising edge of SCK. Modes 1 and 2 latch
data on the falling edge. In modes 2 and 3, SCK is high when idle.
