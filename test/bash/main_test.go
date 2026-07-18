package bash_test

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("WISP_DECK_TESTING") != "1" {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		environment := repositoryTestEnvironment(os.Environ())
		if err := syscall.Exec(executable, os.Args, environment); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}
