package main

import (
	"os"

	"github.com/jackuait/wisp-deck/internal/nativestderr"
)

func main() {
	nativestderr.Shield()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
