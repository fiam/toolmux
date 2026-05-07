package main

import (
	"fmt"
	"os"

	"github.com/fiam/supacli/internal/server"
)

func main() {
	if err := server.NewCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
