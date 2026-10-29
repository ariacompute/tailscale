package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"ariacompute/tailscale/internal/funnel"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := &cobra.Command{
		Use:           "aria-tailscale",
		Short:         "Tailscale Funnel reverse proxy for ariacompute services",
		Long:          "aria-tailscale joins a tailnet via tsnet and exposes internal ariacompute services to the public internet through Tailscale Funnel.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := funnel.FlagsFromCmd(cmd)
			if err != nil {
				return err
			}
			return funnel.Run(cmd.Context(), opts)
		},
	}

	runCmd := &cobra.Command{
		Use:           "run",
		Short:         "Run the Funnel reverse proxy",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := funnel.FlagsFromCmd(cmd)
			if err != nil {
				return err
			}
			return funnel.Run(cmd.Context(), opts)
		},
	}

	funnel.RegisterFlags(root)
	funnel.RegisterFlags(runCmd)
	root.AddCommand(runCmd)

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
