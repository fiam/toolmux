package main

import (
	"fmt"
	"os"

	"github.com/fiam/toolmux/internal/cli"
	_ "github.com/fiam/toolmux/internal/providers/all"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
