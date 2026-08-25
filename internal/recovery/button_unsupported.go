//go:build !linux

package recovery

import "errors"

func openButtonSource(ButtonOptions) (buttonSource, error) {
	return nil, errors.New("gpio recovery requires Linux")
}
