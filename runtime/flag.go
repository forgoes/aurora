package runtime

import (
	"os"

	"github.com/spf13/pflag"
)

type Flags struct {
	ConfigFile string
}

func parseFlags() (*Flags, error) {
	flagSet := pflag.NewFlagSet("identity", pflag.ExitOnError)

	help := false
	flags := &Flags{}

	flagSet.BoolVarP(&help, "help", "h", false, "runbook")
	flagSet.StringVarP(&flags.ConfigFile, "config", "c", "./config.toml", "path of config file")

	if err := flagSet.Parse(os.Args[1:]); err != nil {
		return nil, err
	}

	if help {
		flagSet.PrintDefaults()
		os.Exit(0)
	}

	return flags, nil
}
