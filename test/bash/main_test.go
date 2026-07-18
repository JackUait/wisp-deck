package bash_test

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

const repositoryTestArgv0 = "__WISP_DECK_REPOSITORY_TEST_V1__.test"

func TestMain(m *testing.M) {
	if os.Getenv("WISP_DECK_TESTING") != "1" ||
		len(os.Args) == 0 ||
		os.Args[0] != repositoryTestArgv0 {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		environment := repositoryTestEnvironment(os.Environ())
		arguments := repositoryTestArguments(os.Args)
		if err := syscall.Exec(executable, arguments, environment); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}
