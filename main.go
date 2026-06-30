package main

import "github.com/alikatgh/le-cli/cmd"

// version is overridden at release time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	cmd.Execute(version)
}
