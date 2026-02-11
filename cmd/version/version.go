// Package versioncmder implements the "version" subcommand which prints
// build-time version information.
package versioncmder

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/papercomputeco/stereosd/pkg/version"
)

const versionLongDesc string = `Print stereosd version information.

Displays the version, git commit, build date, and platform.

Examples:
  stereosd version`

const versionShortDesc string = "Print version information"

// NewVersionCmd creates the "version" subcommand.
func NewVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: versionShortDesc,
		Long:  versionLongDesc,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println(version.Info())
			return nil
		},
	}

	return cmd
}
