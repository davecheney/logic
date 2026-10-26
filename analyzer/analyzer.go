// Package analyzer opens the supported logic analysers by name.
package analyzer

import (
	"fmt"
	"strings"

	"github.com/davecheney/logic"
	"github.com/davecheney/logic/bitscope"
	"github.com/davecheney/logic/dslogic"
	"github.com/davecheney/logic/fx2lafw"
	"github.com/davecheney/logic/logicpirate"
	"github.com/davecheney/logic/ols"
)

// driver opens and checks one kind of analyser.
type driver struct {
	name         string
	class        logic.DeviceClass
	open         func(port string, th dslogic.Threshold) (logic.Analyzer, error)
	capabilities func(*logic.Device) logic.CapabilitySet
	validate     func(*logic.Device, logic.CaptureRequest) error
}

func openFX2(string, dslogic.Threshold) (logic.Analyzer, error) { return fx2lafw.Open() }

var drivers = []driver{
	{
		name:         "bitscope",
		class:        logic.ClassBitScope,
		capabilities: bitscope.Capabilities,
		open: func(port string, _ dslogic.Threshold) (logic.Analyzer, error) {
			if port == "" {
				p, err := bitscope.FindPort()
				if err != nil {
					return nil, err
				}
				port = p
			}
			return bitscope.Open(port)
		},
		validate: bitscope.Validate,
	},
	{
		name:         "dslogic",
		class:        logic.ClassDSLogic,
		open:         func(_ string, th dslogic.Threshold) (logic.Analyzer, error) { return dslogic.Open(th) },
		capabilities: dslogic.Capabilities,
		validate:     dslogic.Validate,
	},
	{
		name:         "fx2lafw",
		class:        logic.ClassFX2,
		open:         openFX2,
		capabilities: fx2lafw.Capabilities,
		validate:     fx2lafw.Validate,
	},
	{
		name:         "saleae",
		class:        logic.ClassFX2,
		open:         openFX2,
		capabilities: fx2lafw.Capabilities,
		validate:     fx2lafw.Validate,
	},
	{
		name:         "ols",
		class:        logic.ClassOLS,
		open:         func(port string, _ dslogic.Threshold) (logic.Analyzer, error) { return ols.Open(port) },
		capabilities: ols.Capabilities,
		validate:     ols.Validate,
	},
	{
		name:         "logicpirate",
		class:        logic.ClassLogicPirate,
		open:         func(port string, _ dslogic.Threshold) (logic.Analyzer, error) { return logicpirate.Open(port) },
		capabilities: logicpirate.Capabilities,
		validate:     logicpirate.Validate,
	},
}

// Devices returns the names that Open accepts.
func Devices() []string {
	names := make([]string, len(drivers))
	for i, d := range drivers {
		names[i] = d.name
	}
	return names
}

func lookup(name string) (driver, error) {
	for _, d := range drivers {
		if d.name == name {
			return d, nil
		}
	}
	if name == "" {
		return driver{}, fmt.Errorf("logic: -device is required: %s", strings.Join(Devices(), ", "))
	}
	return driver{}, fmt.Errorf("logic: unknown device %q: use %s", name, strings.Join(Devices(), ", "))
}

// Open opens the named analyser. port selects a BitScope, OLS, or Logic
// Pirate serial port, or searches when empty. th selects the DSLogic threshold.
func Open(name, port string, th dslogic.Threshold) (logic.Analyzer, error) {
	d, err := lookup(name)
	if err != nil {
		return nil, err
	}
	return d.open(port, th)
}

// CapabilitiesFor returns the resources for a device name without opening
// hardware. Its resource IDs describe a synthetic device and cannot address
// an opened analyser.
func CapabilitiesFor(name string) (logic.CapabilitySet, error) {
	d, err := lookup(name)
	if err != nil {
		return logic.CapabilitySet{}, err
	}
	return d.capabilities(logic.NewDevice(d.class)), nil
}

// Validate checks a request for the named analyser without opening it.
func Validate(name string, req logic.CaptureRequest) error {
	d, err := lookup(name)
	if err != nil {
		return err
	}
	var dev *logic.Device
	if len(req.Inputs) > 0 {
		dev = req.Inputs[0].Device()
	}
	if dev == nil {
		dev = req.Trigger.Device()
	}
	if dev == nil {
		dev = logic.NewDevice(d.class)
	}
	if dev.Class() != d.class {
		return fmt.Errorf("logic: request device class %s does not match %s", dev.Class(), d.name)
	}
	return d.validate(dev, req)
}
