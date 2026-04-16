/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package config

import (
	"os"
)

const (
	DirectiveKey = "MODULE_DIRECTIVE"
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
func (rc *RuntimeConfig) Environment() map[string]string {
	return map[string]string{
		DirectiveKey: rc.Directive,
	}
}
