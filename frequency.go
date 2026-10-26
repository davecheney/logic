package logic

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
)

// Frequency is a frequency in milliHertz. Integer values avoid the
// rounding errors of float64, so a rate compares exactly with a table of
// supported rates.
type Frequency uint64

const (
	MilliHertz Frequency = 1
	Hertz                = 1000 * MilliHertz
	KiloHertz            = 1000 * Hertz
	MegaHertz            = 1000 * KiloHertz
	GigaHertz            = 1000 * MegaHertz
)

var frequencyUnits = []struct {
	name string
	unit Frequency
}{
	{"GHz", GigaHertz},
	{"MHz", MegaHertz},
	{"kHz", KiloHertz},
	{"Hz", Hertz},
	{"mHz", MilliHertz},
}

// Hz returns f in Hertz.
func (f Frequency) Hz() float64 { return float64(f) / float64(Hertz) }

// String returns f with the largest unit that keeps the integer part not
// zero, for example "4MHz", "2.5kHz", or "500mHz". Zero is "0Hz".
func (f Frequency) String() string {
	if f == 0 {
		return "0Hz"
	}
	for _, u := range frequencyUnits {
		if f < u.unit {
			continue
		}
		s := strconv.FormatUint(uint64(f/u.unit), 10)
		if frac := f % u.unit; frac != 0 {
			digits := len(strconv.FormatUint(uint64(u.unit), 10)) - 1
			d := strconv.FormatUint(uint64(frac), 10)
			d = strings.Repeat("0", digits-len(d)) + d
			s += "." + strings.TrimRight(d, "0")
		}
		return s + u.name
	}
	panic("unreachable")
}

// ParseFrequency parses a decimal number with an optional unit suffix:
// mHz, Hz, kHz, MHz, or GHz. A number without a unit is in Hertz, so
// "4e6", "4000000", and "4MHz" are the same. The units are case sensitive,
// because mHz and MHz differ. The value must be a whole number of
// milliHertz.
func ParseFrequency(s string) (Frequency, error) {
	num, unit := s, Hertz
	for _, u := range frequencyUnits {
		// Check Hz last, because it is a suffix of the other units.
		if u.unit != Hertz && strings.HasSuffix(s, u.name) {
			num, unit = strings.TrimSuffix(s, u.name), u.unit
			break
		}
	}
	if unit == Hertz {
		num = strings.TrimSuffix(s, "Hz")
	}
	if num == "" || strings.Trim(num, "0123456789.eE+-") != "" || num[0] == '+' || num[0] == '-' {
		return 0, errors.New("logic: invalid frequency " + strconv.Quote(s))
	}
	// A large exponent makes big.Rat allocate a large number.
	if i := strings.IndexAny(num, "eE"); i >= 0 {
		if exp, err := strconv.Atoi(num[i+1:]); err != nil || exp < -30 || exp > 30 {
			return 0, errors.New("logic: invalid frequency " + strconv.Quote(s))
		}
	}
	r, ok := new(big.Rat).SetString(num)
	if !ok {
		return 0, errors.New("logic: invalid frequency " + strconv.Quote(s))
	}
	r.Mul(r, new(big.Rat).SetUint64(uint64(unit)))
	if !r.IsInt() {
		return 0, errors.New("logic: frequency " + strconv.Quote(s) + " is not a whole number of milliHertz")
	}
	if !r.Num().IsUint64() {
		return 0, errors.New("logic: frequency " + strconv.Quote(s) + " is out of range")
	}
	return Frequency(r.Num().Uint64()), nil
}
