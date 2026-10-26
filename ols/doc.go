// Package ols drives the Open Workbench Logic Sniffer with Demon Core
// firmware. The device has USB ID 04d8:fc92 and appears as a USB serial
// port.
//
// The driver finds one matching device. If more than one is connected,
// select the port with Open. You can also select a port when automatic
// discovery does not find the device.
//
// The driver supports the 16 buffered inputs, L0 to L15. It does not
// support the 16 expansion inputs. Connect the analyser ground to the
// target ground. Check the electrical limits for the board before you
// connect an input.
//
// The driver uses the internal 100 MHz clock. It supports sample rates
// from about 5961 samples per second to 100 million samples per second.
// The device reports a 200 MHz maximum rate, but this needs demux mode.
// The driver does not use demux mode.
//
// The sample memory is 24,576 bytes. The device stores one byte for each
// enabled group of eight inputs. One active group supports up to 24,576
// samples. Two active groups support up to 12,288 samples. The device
// rounds the requested sample count up to a multiple of four. The driver
// returns the requested number of samples.
//
// The driver supports rising and falling edge triggers. It does not use
// run-length encoding, an external clock, or the unbuffered expansion
// inputs. It does not configure the FPGA or PIC firmware. It does not
// support 200 MHz demux, RLE compression, or AWG output.
package ols
