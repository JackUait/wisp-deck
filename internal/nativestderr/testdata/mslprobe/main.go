// mslprobe calls the libmalloc entry point that printed "MallocStackLogging:
// can't turn off malloc stack logging" into agent panes, so a test can check
// where that native write lands.
package main

// extern void turn_off_stack_logging(void);
import "C"

import (
	"fmt"
	"os"

	"github.com/jackuait/wisp-deck/internal/nativestderr"
)

func main() {
	if os.Getenv("MSLPROBE_SHIELD") == "1" {
		nativestderr.Shield()
	}
	C.turn_off_stack_logging()
	fmt.Fprintln(os.Stderr, "go-stderr-still-visible")
	if os.Getenv("MSLPROBE_PANIC") == "1" {
		panic("probe-panic-still-visible")
	}
}
