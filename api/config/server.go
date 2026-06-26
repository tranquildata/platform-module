/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package config

import (
	"os"
	"strings"
)

const (
	DirectiveKey  = "MODULE_DIRECTIVE"
	BatchInputKey = "BATCH_INPUTS"
)

type RuntimeConfig struct {
	Directive string
}

// ServerConfig extracts all expected environment variable configuration.
func ServerConfig() (*RuntimeConfig, error) {
	directive := ""

	if directiveStr, present := os.LookupEnv(DirectiveKey); present {
		directive = directiveStr
	}

	return &RuntimeConfig{
		Directive: directive,
	}, nil
}

// Environment returns the full environment that should be shared with the
// wrapper script .. note that currently this only includes the specific variables
// the API service uses, but this could expand to include the full ENV.
func (rc *RuntimeConfig) Environment(inputFiles ...string) map[string]string {
	envMap := map[string]string{
		DirectiveKey: rc.Directive,
	}
	if len(inputFiles) > 0 {
		envMap[BatchInputKey] = strings.Join(inputFiles, " ")
	}

	return envMap
}
