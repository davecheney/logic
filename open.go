package logic

import (
	"fmt"
	"strings"
)

// driver opens and checks one kind of analyser.
type driver struct {
	name     string
	open     func(port string, th Threshold) (Analyzer, error)
	validate func(Config) error
}

var drivers = []driver{
	{
		name: "bitscope",
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
		validate: (&BitScope{}).ValidateLogic,
	},
	{
		name:     "dslogic",
		open:     func(_ string, th Threshold) (Analyzer, error) { return OpenDSLogic(th) },
		validate: (&DSLogic{}).ValidateLogic,
	},
	{
		name:     "fx2lafw",
		open:     func(_ string, _ Threshold) (Analyzer, error) { return OpenFX2() },
		validate: (&FX2{}).ValidateLogic,
	},
	{
		name:     "saleae",
		open:     func(_ string, _ Threshold) (Analyzer, error) { return OpenFX2() },
		validate: (&FX2{}).ValidateLogic,
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

// Open opens the named analyser. port selects the BitScope serial port, or
// searches for one when empty. th selects the DSLogic input threshold.
func Open(name, port string, th Threshold) (Analyzer, error) {
	d, err := lookup(name)
	if err != nil {
		return nil, err
	}
	return d.open(port, th)
}

// Validate checks c for the named analyser without opening it.
func Validate(name string, c Config) error {
	d, err := lookup(name)
	if err != nil {
		return err
	}
	return d.validate(c)
}
