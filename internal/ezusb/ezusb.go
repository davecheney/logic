// Package ezusb loads firmware into Cypress EZ-USB FX2 devices.
package ezusb

import (
	"fmt"
	"time"

	"github.com/google/gousb"
)

const (
	// VendorOut is the request type of a vendor request to the device.
	VendorOut = gousb.ControlVendor | gousb.ControlDevice | gousb.ControlOut
	// VendorIn is the request type of a vendor request from the device.
	VendorIn = gousb.ControlVendor | gousb.ControlDevice | gousb.ControlIn
)

// reqFX2Load is the Cypress EZ-USB vendor request that writes FX2 RAM.
const reqFX2Load = 0xa0

// LoadFirmware holds the FX2 CPU in reset, writes fw to its RAM, and lets
// it run. The device then disconnects and enumerates again.
func LoadFirmware(dev *gousb.Device, fw []byte) error {
	dev.ControlTimeout = 3 * time.Second
	reset := func(on byte) error {
		_, err := dev.Control(VendorOut, reqFX2Load, 0xe600, 0, []byte{on})
		return err
	}
	if err := reset(1); err != nil {
		return fmt.Errorf("FX2 reset: %w", err)
	}
	for off := 0; off < len(fw); off += 4096 {
		chunk := fw[off:min(off+4096, len(fw))]
		if _, err := dev.Control(VendorOut, reqFX2Load, uint16(off), 0, chunk); err != nil {
			return fmt.Errorf("firmware write at %#x: %w", off, err)
		}
	}
	if err := reset(0); err != nil {
		return fmt.Errorf("FX2 run: %w", err)
	}
	return nil
}
