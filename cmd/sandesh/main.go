// sandesh: Gmail, Google Chat and WhatsApp in the terminal, with an MCP server so
// AI agents can read, draft and (with your OK) send.
package main

import (
	"os"

	"github.com/Theyashsawarkar/sandesh/internal/cli"
	"github.com/Theyashsawarkar/sandesh/internal/tui"
)

func main() {
	cli.ApproveScreen = tui.Approve
	os.Exit(cli.Run(os.Args[1:], tui.Run))
}
