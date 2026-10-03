package invocation

import (
	"fmt"
	"os"
)

func lock(_ *os.File) error {
	return fmt.Errorf("native invocation recording is unsupported on Windows")
}
func unlock(_ *os.File) {}
