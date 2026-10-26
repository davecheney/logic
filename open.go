package logic

import (
	"fmt"
	"strings"
)

// driver opens and checks one kind of analyser.
type driver struct {
	name         string
	class        DeviceClass
	open         func(port string, th Threshold) (Analyzer, error)
	capabilities func(*device) CapabilitySet
	validate     func(logicConfig) error
}

var drivers = []driver{
	{
		name:         "bitscope",
		class:        ClassBitScope,
		capabilities: bitscopeCapabilities,
		open: func(port string, _ Threshold) (Analyzer, error) {
			if port == "" {
				p, err := FindPort()
				if err != nil {
					return nil, err
				}
				port = p
			}
			return OpenBitScope(port)
		},
		validate: (&BitScope{}).validateLogic,
	},
	{
		name:         "dslogic",
		class:        ClassDSLogic,
		open:         func(_ string, th Threshold) (Analyzer, error) { return OpenDSLogic(th) },
		capabilities: dslogicCapabilities,
		validate:     (&DSLogic{}).validateLogic,
	},
	{
		name:         "fx2lafw",
		class:        ClassFX2,
		open:         func(_ string, _ Threshold) (Analyzer, error) { return OpenFX2() },
		capabilities: fx2Capabilities,
		validate:     (&FX2{}).validateLogic,
	},
	{
		name:         "saleae",
		class:        ClassFX2,
		open:         func(_ string, _ Threshold) (Analyzer, error) { return OpenFX2() },
		capabilities: fx2Capabilities,
		validate:     (&FX2{}).validateLogic,
	},
	{
		name:         "ols",
		class:        ClassOLS,
		open:         func(port string, _ Threshold) (Analyzer, error) { return OpenOLS(port) },
		capabilities: olsCapabilities,
		validate:     validateOLSLogic,
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

// Open opens the named analyser. port selects a serial port for the BitScope
// or OLS, or searches for one when empty. th selects the DSLogic threshold.
func Open(name, port string, th Threshold) (Analyzer, error) {
	d, err := lookup(name)
	if err != nil {
		return nil, err
	}
	return d.open(port, th)
}

// CapabilitiesFor returns the resources for a device name without opening
// hardware. Its resource IDs describe a synthetic device and cannot address
// an opened analyser.
func CapabilitiesFor(name string) (CapabilitySet, error) {
	d, err := lookup(name)
	if err != nil {
		return CapabilitySet{}, err
	}
	return d.capabilities(newDevice(d.class)), nil
}

// Validate checks a request for the named analyser without opening it.
func Validate(name string, req CaptureRequest) error {
	d, err := lookup(name)
	if err != nil {
		return err
	}
	var dev *device
	if len(req.Inputs) > 0 {
		dev = req.Inputs[0].Device()
	}
	if dev == nil {
		dev = req.Trigger.Device()
	}
	if dev == nil {
		dev = &device{class: d.class}
	}
	if dev.class != d.class {
		return fmt.Errorf("logic: request device class %s does not match %s", dev.class, d.name)
	}
	c, err := captureConfig(dev, d.capabilities(dev), req)
	if err != nil {
		return err
	}
	return d.validate(c)
}
