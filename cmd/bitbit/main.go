package main

import (
	"fmt"
	"os"

	"github.com/mildlybrutal/bitbit/internal/bitbit"
)

func main() {
	if err := bitbit.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
