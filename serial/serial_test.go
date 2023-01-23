package serial

import (
	"os"
	"testing"

	"go.viam.com/test"
)

func TestOpen(t *testing.T) {
	_, err := Open("")
	test.That(t, err, test.ShouldNotBeNil)
	test.That(t, os.IsNotExist(err), test.ShouldBeTrue)
}
