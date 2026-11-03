// Command prstk manages stacks of GitHub PRs worked in parallel by agents.
package main

import (
	"fmt"
	"os"

	"prstk/internal/cli"
)

func main() {
	app, err := cli.NewDefault()
	if err != nil {
		fmt.Fprintln(os.Stderr, "prstk:", err)
		os.Exit(1)
	}
	os.Exit(app.Run(os.Args[1:]))
}
