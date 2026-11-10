//go:build !linux && !darwin

package doubles

import (
	"errors"
	"os"
)

func openPTY() (*os.File, *os.File, error) {
	return nil, nil, errors.New("not supported on this platform")
}
