package web

import (
	"strings"
	"testing"
)

func TestValidPSK(t *testing.T) {
	hex64 := strings.Repeat("0123456789abcdef", 4)

	tests := []struct {
		name     string
		password string
		expected bool
	}{
		{name: "empty", password: "", expected: false},
		{name: "seven bytes is too short", password: strings.Repeat("a", 7), expected: false},
		{name: "eight bytes is the minimum", password: strings.Repeat("a", 8), expected: true},
		{name: "sixty three bytes is the maximum", password: strings.Repeat("a", 63), expected: true},
		{name: "sixty five bytes is rejected", password: strings.Repeat("a", 65), expected: false},

		// Exactly 64 bytes is the raw PMK form and must be pure hexadecimal.
		{name: "sixty four lowercase hex digits", password: hex64, expected: true},
		{name: "sixty four uppercase hex digits", password: strings.ToUpper(hex64), expected: true},
		{
			name:     "sixty four mixed case hex digits",
			password: strings.Repeat("0123456789abcdefABCDEF0123456789", 2),
			expected: true,
		},
		{
			name:     "sixty four bytes with a non hex letter",
			password: strings.Repeat("g", 64),
			expected: false,
		},
		{
			name:     "sixty four bytes with one non hex character",
			password: hex64[:63] + "z",
			expected: false,
		},
		// A multi-byte rune keeps the byte length at 64 while making the string
		// 63 runes long. Both the byte-oriented and rune-oriented reading must
		// reject it.
		{
			name:     "sixty four bytes containing a multi byte rune",
			password: hex64[:62] + "é",
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validPSK(test.password); got != test.expected {
				t.Fatalf(
					"validPSK(%d bytes) = %t, expected %t",
					len(test.password),
					got,
					test.expected,
				)
			}
		})
	}
}
