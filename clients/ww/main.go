// Command ww is the command-line companion for the Witwave / witwave agent
// platform. It talks to a harness over the shared event + REST surface:
// tail the live event stream, send A2A prompts, inspect scheduler
// configuration, and validate scheduler files — all without a browser.
//
// Version info is injected at build time via -ldflags:
//
//	go build -ldflags "-X 'github.com/witwave-ai/witwave/clients/ww/cmd.Version=0.1.0' \
//	                   -X 'github.com/witwave-ai/witwave/clients/ww/cmd.Commit=<sha>' \
//	                   -X 'github.com/witwave-ai/witwave/clients/ww/cmd.BuildDate=<iso>'" \
//	  .
package main

import (
	"os"

	"github.com/witwave-ai/witwave/clients/ww/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
