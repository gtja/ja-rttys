package log

import (
	"os"
	"os/exec"
	"testing"
)

func TestRecoverPanicDoesNotExitProcess(t *testing.T) {
	if os.Getenv("RTTYS_PANIC_TEST") == "1" {
		func() { defer RecoverPanic(); panic("isolated connection failure") }()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoverPanicDoesNotExitProcess$")
	cmd.Env = append(os.Environ(), "RTTYS_PANIC_TEST=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("connection panic killed process: %v\n%s", err, output)
	}
}
