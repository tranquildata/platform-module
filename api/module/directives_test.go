/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package module

import (
	"os"
	"testing"
)

func Test_thatDirectivesParse(t *testing.T) {
	dm, err := Parse([]byte(`[
		{"name": "hello", "streaming": true},
		{"name": "montecarlo", "streaming": false},
		{"name": "simulate"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		present   bool
		streaming bool
	}{
		{"hello", true, true},
		{"montecarlo", true, false},
		{"simulate", true, false}, //streaming defaults to batch
		{"unknown", false, false},
	}
	for _, c := range cases {
		if present, streaming := dm.Config(c.name); present != c.present || streaming != c.streaming {
			t.Errorf("%s: expected (%t, %t), got (%t, %t)", c.name, c.present, c.streaming, present, streaming)
		}
	}
}

func Test_thatInvalidDirectivesFail(t *testing.T) {
	cases := map[string]string{
		"duplicate name": `[{"name": "hello", "streaming": true}, {"name": "hello", "streaming": false}]`,
		"missing name":   `[{"streaming": true}]`,
		"old syntax":     `[{"name": "hello", "batch": {"files": false}}]`,
		"not a list":     `{"name": "hello", "streaming": true}`,
	}
	for description, input := range cases {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("%s: expected an error", description)
		}
	}
}

func Test_thatShippedDirectivesParse(t *testing.T) {
	for _, filename := range []string{"../../directives.json", "../../examples/fileio/directives.json"} {
		jsonBytes, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Parse(jsonBytes); err != nil {
			t.Errorf("%s: %s", filename, err.Error())
		}
	}
}
