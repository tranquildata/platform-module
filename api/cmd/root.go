/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package cmd

import (
	"github.com/spf13/cobra"
)

// commands lists all of the available commands for runing the API server
var commands = []func() *cobra.Command{
	startCommand,
}

// Execute is called to handle input and start a specific command.
func Execute() error {
	rootCmd := &cobra.Command{Use: "platform-module"}
	for _, cmd := range commands {
		rootCmd.AddCommand(cmd())
	}
	return rootCmd.Execute()
}
