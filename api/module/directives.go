/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// DirectivesFile is the staged file defining all available directives
const DirectivesFile = "/moduleio/directives.json"

type DirectiveMap struct {
	directives map[string]bool
}

type Directive struct {
	Name      string `json:"name"`
	Streaming bool   `json:"streaming"`
}

// Load attempts to read & process the directives file.
func Load() (*DirectiveMap, error) {
	jsonBytes, err := os.ReadFile(DirectivesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read directives: %s", err.Error())
	}
	return Parse(jsonBytes)
}

// Parse processes the contents of a directives file. Unknown fields are rejected
// so that files using an older syntax fail loudly rather than being misread, and
// directive names must be unique.
func Parse(jsonBytes []byte) (*DirectiveMap, error) {
	decoder := json.NewDecoder(bytes.NewReader(jsonBytes))
	decoder.DisallowUnknownFields()

	var directives []Directive
	if err := decoder.Decode(&directives); err != nil {
		return nil, fmt.Errorf("failed to process directives: %s", err.Error())
	}

	directiveMap := make(map[string]bool, len(directives))
	for _, directive := range directives {
		if directive.Name == "" {
			return nil, fmt.Errorf("failed to process directives: directive is missing a name")
		} else if _, present := directiveMap[directive.Name]; present {
			return nil, fmt.Errorf("failed to process directives: duplicate directive %s", directive.Name)
		}
		directiveMap[directive.Name] = directive.Streaming
	}

	return &DirectiveMap{
		directives: directiveMap,
	}, nil
}

// Config returns (in-order) whether the directive is present, and if it is present
// then whether it is a streaming (standard IO) or batch (file IO) configuration
func (dm *DirectiveMap) Config(name string) (bool, bool) {
	streaming, present := dm.directives[name]
	return present, streaming
}
