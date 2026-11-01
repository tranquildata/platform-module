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
	directives map[string]*Directive
	ordered    []Directive //in the order defined in the file
}

type Directive struct {
	Name       string      `json:"name"`
	Streaming  bool        `json:"streaming"`
	Parameters []Parameter `json:"parameters,omitempty"`
}

type Parameter struct {
	Name     string   `json:"name"`
	Required bool     `json:"required"`
	Default  *float64 `json:"default,omitempty"` //nil when no default is defined
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
// so that files using an older syntax fail loudly rather than being misread.
// Directive names must be unique, as must parameter names within a directive.
func Parse(jsonBytes []byte) (*DirectiveMap, error) {
	decoder := json.NewDecoder(bytes.NewReader(jsonBytes))
	decoder.DisallowUnknownFields()

	var directives []Directive
	if err := decoder.Decode(&directives); err != nil {
		return nil, fmt.Errorf("failed to process directives: %s", err.Error())
	}

	directiveMap := make(map[string]*Directive, len(directives))
	for idx := range directives {
		directive := &directives[idx]
		if directive.Name == "" {
			return nil, fmt.Errorf("failed to process directives: directive is missing a name")
		} else if _, present := directiveMap[directive.Name]; present {
			return nil, fmt.Errorf("failed to process directives: duplicate directive %s", directive.Name)
		}
		parameterNames := make(map[string]bool, len(directive.Parameters))
		for _, parameter := range directive.Parameters {
			if parameter.Name == "" {
				return nil, fmt.Errorf("failed to process directives: parameter of directive %s is missing a name", directive.Name)
			} else if parameterNames[parameter.Name] {
				return nil, fmt.Errorf("failed to process directives: duplicate parameter %s in directive %s", parameter.Name, directive.Name)
			}
			parameterNames[parameter.Name] = true
		}
		directiveMap[directive.Name] = directive
	}

	return &DirectiveMap{
		directives: directiveMap,
		ordered:    directives,
	}, nil
}

// Config returns (in-order) whether the directive is present, and if it is present
// then whether it is a streaming (standard IO) or batch (file IO) configuration
func (dm *DirectiveMap) Config(name string) (bool, bool) {
	if directive, present := dm.directives[name]; present {
		return true, directive.Streaming
	}
	return false, false
}

// Directives returns all directives in the order they are defined.
func (dm *DirectiveMap) Directives() []Directive {
	directives := make([]Directive, len(dm.ordered))
	copy(directives, dm.ordered)
	return directives
}

// Parameters returns (in-order) the parameters defined for the directive, which
// is empty but non-nil if it defines none, and whether the directive is present
func (dm *DirectiveMap) Parameters(name string) ([]Parameter, bool) {
	directive, present := dm.directives[name]
	if !present {
		return nil, false
	}
	parameters := make([]Parameter, len(directive.Parameters))
	copy(parameters, directive.Parameters)
	return parameters, true
}
