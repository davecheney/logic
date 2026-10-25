package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestInvalidOptions(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "-device is required"},
		{[]string{"-device", "saleae"}, "unknown device"},
		{[]string{"-device", "dslogic", "-threshold", "1.8"}, "threshold must be 3.3 or 5"},
		{[]string{"-device", "bitscope", "-port", "/dev/null", "-baud", "fast"}, "bad baud rate"},
		{[]string{"-device", "bitscope", "extra"}, "unexpected arguments"},
	} {
		var out bytes.Buffer
		err := run(tc.args, &out, &out)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args %v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}
