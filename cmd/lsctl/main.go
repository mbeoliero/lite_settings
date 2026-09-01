// Command lsctl manages lite_settings through HTTP or a direct database connection.
package main

import (
	"os"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/command"
)

func main() { os.Exit(command.Execute()) }
