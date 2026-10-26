package bitscope

import (
	"errors"
	"fmt"
	"strings"

	"go.bug.st/serial/enumerator"
)

// FindPort finds one FTDI port. Open checks the device ID.
func FindPort() (string, error) {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return "", err
	}
	var names []string
	for _, p := range ports {
		if p.IsUSB && strings.EqualFold(p.VID, "0403") && strings.EqualFold(p.PID, "6001") &&
			!strings.HasPrefix(p.Name, "/dev/tty.") {
			names = append(names, p.Name)
		}
	}
	if len(names) == 1 {
		return names[0], nil
	}
	if len(names) > 1 {
		return "", fmt.Errorf("multiple FTDI devices found; use -port: %s", strings.Join(names, ", "))
	}
	return "", errors.New("no BitScope found; use -port")
}
