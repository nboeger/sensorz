package model

import (
	"fmt"
	"math"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// shortHz converts hertz to the largest unit that keeps the number readable,
// so 4_920_000_000Hz prints as "4920MHz" rather than a 10 digit number.
func shortHz(hz float64) float64 {
	switch {
	case hz >= 1e9:
		return hz / 1e9 * 1e3
	case hz >= 1e6:
		return hz / 1e6
	default:
		return hz / 1e3
	}
}

var byteUnits = []string{"B", "KB", "MB", "GB", "TB", "PB"}

// HumanBytes renders a byte count with a binary-prefixed unit suffix.
func HumanBytes(b float64) string {
	neg := ""
	if b < 0 {
		neg, b = "-", -b
	}
	i := 0
	for b >= 1024 && i < len(byteUnits)-1 {
		b /= 1024
		i++
	}
	switch {
	case i == 0:
		return neg + fmt.Sprintf("%.0f%s", b, byteUnits[i])
	case b >= 100:
		return neg + fmt.Sprintf("%.0f%s", b, byteUnits[i])
	case b >= 10:
		return neg + fmt.Sprintf("%.1f%s", b, byteUnits[i])
	default:
		return neg + fmt.Sprintf("%.2f%s", b, byteUnits[i])
	}
}

// Clamp constrains v to [lo, hi].
func Clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }
