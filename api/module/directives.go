/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package module

import (
	"encoding/json"
	"fmt"
	"os"
)

// DirectivesFile is the staged file defining all available directives
const DirectivesFile = "/moduleio/directives.json"

type DirectiveMap struct {
	directives map[string]*Batch
}

type Directive struct {
	Name        string `json:"name"`
	BatchConfig *Batch `json:"batch,omitempty"`
}

type Batch struct {
	Files bool `json:"files"`
}

// Load attempts to read & process the directives file.
func Load() (*DirectiveMap, error) {
	jsonBytes, err := os.ReadFile(DirectivesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read directives: %s", err.Error())
	}

	var directives []Directive
	if err = json.Unmarshal(jsonBytes, &directives); err != nil {
		return nil, fmt.Errorf("failed to process directives: %s", err.Error())
	}

	directiveMap := map[string]*Batch{}
	for _, directive := range directives {
		directiveMap[directive.Name] = directive.BatchConfig
	}

	return &DirectiveMap{
		directives: directiveMap,
	}, nil
}

// Config returns (in-order) whether the directive is present, if it is present then
// whether modality is batch, and if it is batch then whether file IO is used
func (dm *DirectiveMap) Config(name string) (bool, bool, bool) {
	if config, present := dm.directives[name]; !present {
		return false, false, false
	} else if config == nil {
		return true, false, false
	} else {
		return true, true, config.Files
	}
}
