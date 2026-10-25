# logic-i2c

`logic-i2c` decodes I2C traffic in a `logic-capture` JSON file, one line
per transaction.

```sh
logic-capture -device dslogic -rate 10000000 -samples 1000000 \
  -channels 0x60 -trigger 5 -falling -output i2c.json
logic-i2c -input i2c.json -sda 5 -scl 6
```

Capture at 10 or more samples per SCL period. At 100 kHz, 1 MS/s is
enough. `-sda` and `-scl` are required.

## Output

Each line starts at a START and ends at the STOP:

```
+0.008266500 S 3c W A 00 A Sr 3c R A 42 A 42 N P
+0.060862000 S 08 R N P
```

The first column is the time of the START in seconds relative to the
trigger. Then:

| Token | Meaning |
|---|---|
| `S` | START |
| `Sr` | repeated START |
| `3c W` or `3c R` | 7-bit address in hex, then write or read |
| `00` | data byte in hex |
| `A` or `N` | ACK or NACK of the byte before it |
| `P` | STOP |
| `?` | a byte cut short by a START or STOP |

A line with no `P` ran past the end of the capture. The SCL rise that sets
up a STOP or repeated START is not counted as a data bit.
