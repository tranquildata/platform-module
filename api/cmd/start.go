/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tranquildata/platform-module/config"
	"github.com/tranquildata/platform-module/module"
	"github.com/tranquildata/platform-module/server"
)

// startCommand is the handler for the "start" CLI command
func startCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start serving the module API interface",
		Run: func(cmd *cobra.Command, args []string) {
			port, err := cmd.Flags().GetUint16("port")
			if err != nil {
				fmt.Printf("failed to resolve port: %s\n", err.Error())
				return
			}
			runStartCommand(port)
		},
	}

	// following best-practices, command-line flags are used to configure aspects
	// of the instance that are also defined in container management, but other
	// configuration is done via environment variables
	cmd.Flags().Uint16P("port", "p", 80, "Port to accept API connections")

	return cmd
}

func runStartCommand(port uint16) {
	// make sure we can get the expected configuration via the environment
	runtimeConfig, err := config.ServerConfig()
	if err != nil {
		fmt.Printf("invalid environment: %s\n", err.Error())
		return
	}

	// resolve the module directives and see what mode we're running in
	directiveMap, err := module.Load()
	if err != nil {
		fmt.Printf("failed to load module directives: %s\n", err.Error())
		return
	}
	present, batch, fileIO := directiveMap.Config(runtimeConfig.Directive)
	if !present {
		fmt.Printf("unknown module directive: %s\n", runtimeConfig.Directive)
		return
	}

	// setup, run, and wait for the API service to terminate
	apiService := server.Setup(runtimeConfig, batch, fileIO)
	if err = apiService.Serve(port); err != nil {
		fmt.Printf("server failed: %s\n", err.Error())
		return
	}
}
