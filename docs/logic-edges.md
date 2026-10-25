# logic-edges

`logic-edges` lists the level changes in a `logic-capture` JSON file, one
line per change.

```sh
logic-capture -device dslogic -rate 10000000 -samples 200000 \
  -channels 0x7 -trigger 1 -pre 0.4 -output cap.json
logic-edges -input cap.json -channels 0x6 -from -1ms -to 2ms
```

## Output

The first line starts with `#`. It holds the sample rate, the trigger
position, and the level of each selected input at the start of the window.
Each following line is one change:

```
# sample_rate_hz=1e+07 trigger_sample=80020 triggered=true start=-0.001000000 L1=0 L2=1
+0.000000000 L1 1
+0.000125300 L2 0
```

The columns are the time in seconds relative to the trigger, the input,
and its new level. When several inputs change in the same sample, they
are listed from L0 up.

## Options

`-channels` is a bit mask of the inputs to list. The default is every
input in the capture. `-from` and `-to` limit the window to times relative
to the trigger, such as `-100us` or `2ms`. `-limit` stops after that many
changes. `-input -` reads the capture from stdin.
