package pexec

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	testutilsext "go.viam.com/utils/testutils/ext"
)

// TestMain is used to control the execution of all tests run within this package (including _test packages).
func TestMain(m *testing.M) {
	if _, err := exec.LookPath("bash"); err != nil {
		fmt.Fprintf(os.Stderr, "failed to find bash for testing: %v", err)
		os.Exit(1)
	}
	testutilsext.VerifyTestMain(m)
}
