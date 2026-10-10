// Command dfm-mcp is the DFM check engine as an MCP server on the user's machine: checks run in this process and drawings stay here.
package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	ctx := context.Background()
	if err := Run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}
