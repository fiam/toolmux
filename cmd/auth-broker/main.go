package main

import (
	"fmt"
	"os"

	"github.com/fiam/supacli/internal/broker"
)

func main() {
	if err := broker.NewCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
