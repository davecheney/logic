// Package fx2lafw drives eight-channel, 24 MHz Saleae Logic clones (USB ID
// 0925:3881), including the Lonely Binary USB analyser. These are a
// Cypress FX2 with no FPGA. The driver loads the sigrok fx2lafw firmware
// into FX2 RAM and streams samples over USB.
//
// Use -device saleae or -device fx2lafw with the commands. No Saleae or
// sigrok software is necessary. Connect only one such analyser. Close
// PulseView and other capture programs before use.
//
// The protocol follows the sigrok libsigrok fx2lafw driver. The firmware
// in the firmware directory comes from sigrok-firmware-fx2lafw; see
// THIRD_PARTY_NOTICES.
//
// # Install and connect
//
// The tools need libusb (brew install libusb on macOS). On Linux, add the
// udev rule in [docs/linux.md].
//
//	logic-probe -device saleae
//
// The analyser has no firmware of its own. When necessary, the tools load
// the sigrok fx2lafw firmware into the RAM of the analyser. The firmware
// is part of the tools. It does not change the USB EEPROM. Unplug the
// analyser to remove it.
//
// Connect analyser GND to target GND. Device inputs D0 to D7 are L0 to L7
// in the output. Check the voltage limits of your specific clone before
// you connect it. -threshold applies only to DSLogic; it does not change
// the FX2 input voltage limits.
//
// # Capture
//
//	logic-capture -device saleae -channels 0xff -rate 24000000 \
//	  -samples 240000 -trigger 0 -auto 100ms -format vcd -output capture.vcd
//
// The supported rates, in samples per second, are:
//
//	20000 25000 50000 100000 200000 250000 500000
//	1000000 2000000 3000000 4000000 6000000 8000000
//	12000000 16000000 24000000
//
// Rates are exact. Other rates are rejected. Capture at least two samples.
// Sample storage uses one byte per sample in host memory. Disabled input
// bits are zero. All eight inputs are sampled together.
//
// Triggers are detected in software from the continuous sample stream.
// Use -trigger and -falling as for the other analysers. -pre must be
// at least zero and less than one. The driver first collects enough
// pre-trigger samples, then accepts an edge. An earlier edge is ignored.
//
// -auto measures sampled signal time from the start of the stream. It
// does not include USB startup delay. If no edge occurs, the
// driver uses an auto trigger after that interval and after it has enough
// pre-trigger data. The output has triggered=false. -wait still limits
// the total capture time.
//
// The analyser has no large capture memory. The USB connection and host
// must keep up with the selected rate. The firmware does not report lost
// samples. If the host does not keep up, the trace can have gaps. Use a
// lower rate or a direct USB port if a trace is not correct.
//
// [docs/linux.md]: https://github.com/davecheney/logic/blob/main/docs/linux.md
package fx2lafw
