package cli

import (
	"errors"
	"flag"
	"io"
	"strings"
)

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("hiraishin "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			var b strings.Builder
			fs.SetOutput(&b)
			fs.PrintDefaults()
			fs.SetOutput(io.Discard)
			return usageError{msg: "flags:\n" + b.String()}
		}
		return usageError{msg: err.Error()}
	}
	return nil
}
