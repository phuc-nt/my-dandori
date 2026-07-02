package main

import (
	"fmt"
	"os"

	"github.com/phuc-nt/dandori/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "dandori:", err)
		os.Exit(1)
	}
}
