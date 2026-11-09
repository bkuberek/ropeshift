//go:build !postgres

package contract

import (
	"os"
	"testing"

	"ropeshift.com/ropeshift/test/doubles"
)

func TestMain(m *testing.M) {
	os.Exit(doubles.Main(m))
}
