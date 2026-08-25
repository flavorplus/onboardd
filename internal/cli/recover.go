package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/flavorplus/onboardd/internal/recovery"
)

func runRecover(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("recover", stderr)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if err := recovery.RequestControl(ctx, recovery.ControlSocketPath); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "manual recovery requested")
	return nil
}
