// Package logicpirate drives the Dangerous Prototypes Logic Pirate with
// stock 40 MHz or 60 MHz firmware. The device has USB ID 04d8:fa95 and
// appears as a USB serial port. Open searches for one connected device
// when the port is empty.
//
// The driver supports eight inputs, L0 to L7. Connect the analyser ground
// to the target ground. Check the board's electrical limits before you
// connect an input.
//
// Both firmware variants support 2, 5, 10, 20, 50, 100, 200 and 500 kHz,
// and 1, 2, 5 and 10 MHz. The 40 MHz variant also supports 20 and 40 MHz.
// The 60 MHz variant instead supports 15, 30 and 60 MHz. Only these rates
// are accepted. The firmware silently uses 10 MHz for other dividers, so
// the driver rejects them. Capabilities before Open lists both variants;
// the open device reports and checks its actual rates.
//
// The firmware supports only any-edge triggers. Set CaptureRequest.AnyEdge
// or use logic-capture -any-edge. Rising-only and falling-only requests
// are rejected. The trigger position is approximate because the firmware
// uses a change-notice interrupt, not a sample-clock trigger.
//
// The firmware enables the trigger when it arms and does not first fill
// the pre-trigger buffer. Samples from before the arm are a fill pattern,
// not input data. The driver therefore rejects a non-zero pre-trigger
// fraction; use logic-capture -pre 0. The trigger is the first sample.
//
// Captures have 4,096 to 245,760 samples, for any input selection. The
// firmware sends no final short USB packet. On macOS, a partial 16 KiB
// transfer stays buffered. The driver rounds transfers up to 16 KiB,
// then returns the requested number of samples. The full 262,144-sample
// buffer is not used because the firmware's delay does not work at that
// size. Samples arrive oldest first. Disabled input bits are cleared.
//
// Auto waits for an edge and the post-trigger samples. If no data arrives,
// the driver stops and starts a new capture without a trigger. This trace
// has Triggered=false.
// Reset includes a 500 ms wait for the firmware's delayed RAM fill. The
// overall context deadline covers the wait and the new capture.
//
// The driver does not change the firmware or configure the TEST output.
//
// To run the hardware checks, connect the stock TEST output to input 0,
// then run:
//
//	LOGICPIRATE_TEST_PORT=/dev/cu.usbmodem12101 go test ./logicpirate -run TestHardware -v
//
// Replace the port name with the connected device's port.
//
// Protocol reference:
// https://github.com/DangerousPrototypes/Logic_Pirate/blob/1b1cea48fca61abf208455fc577ee36b3d986df4/firmware/main.c
package logicpirate
