// Package bitscope drives the BitScope Micro (BS05) as a logic analyser.
//
// Use -device bitscope with the commands.
//
//	logic-capture -device bitscope -rate 4000000 -samples 12000 \
//	  -channels 3 -trigger 1 -pre 0.05 -format csv -output capture.csv
//
// -port selects the serial port. Without it, the tools search for one.
//
// # Inputs
//
// Use the six direct inputs, L0 through L5. The BS05 can also use the two
// analog comparators as logic inputs. These tools do not enable those
// comparator inputs.
//
// Close other BitScope programs before you capture. Connect the signal
// ground to a BitScope ground pin. Use known 3.3 V signals for the initial
// check. Check the hardware manual for input ratings before you connect
// other signals. L4 shares the AWG pin. L5 shares the clock output pin.
// Keep both outputs off when you connect external signals. The logic tools
// do not enable either output.
//
// # Limits
//
// One byte holds all selected inputs. The 12 KiB buffer holds at most 12288
// samples, whether one or six inputs are selected. The tools use normal
// logic mode, from about 2442 S/s through 8 MS/s. Faster hardware modes are
// not enabled. The actual rate is rounded down to a device clock step.
// Pre-trigger capture requires a rate of at most 5 MS/s. The capture and
// auto timeout must fit within the 419 ms hardware timer.
//
// # Probe
//
// logic-probe sends only VM reset (!) and identify (?).
//
//	logic-probe -device bitscope
//
// The connected device returned "!\x3f\rBS000500\r" across the two commands.
// It replied at both 460800 and 115200 baud. The FT245R uses a parallel FIFO,
// not a UART, so these results do not select a UART baud rate.
//
// # Checks on hardware
//
// Digital capture has been checked on BS000500 with a 1 MHz SPI signal.
// At 4 MS/s, clock periods within each byte span four samples. The L1
// rising-edge trigger appeared at the requested sample 600. L0 captured
// 512 clock pulses. The corresponding L2 data decoded to bytes 0x40 through
// 0x7F in an external check.
//
// The hardware tests need a connected BitScope:
//
//	BITSCOPE_PORT=/dev/ttyUSB0 go test -v -count=1 -run '^TestDevice$' ./bitscope
//	BITSCOPE_LOGIC_PORT=/dev/ttyUSB0 go test -v -count=1 -run '^TestLogicDevice$' ./bitscope
//
// The BITSCOPE_PORT tests change capture settings but do not program the
// output generator.
//
// # References
//
//   - [BitScope VM]
//   - [BitLib guide]
//   - [scopething]: register map, capture modes, and nominal conversion
//     values. See THIRD_PARTY_NOTICES.
//
// [BitScope VM]: https://docs.bitscope.com/virtual-machine/
// [BitLib guide]: https://docs.bitscope.com/UZWFJ5CE/
// [scopething]: https://github.com/jonathanhogg/scopething
package bitscope
