package main

import (
	"fmt"
	"os"

	_ "github.com/fiam/toolmux/internal/providers/brokers/all"
	"github.com/fiam/toolmux/internal/server"
)

func main() {
	if err := server.NewCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
