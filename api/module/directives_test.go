/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package module

import (
	"os"
	"reflect"
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

func Test_thatParametersParse(t *testing.T) {
	dm, err := Parse([]byte(`[
		{"name": "montecarlo", "parameters": [
			{"name": "iterations", "required": true},
			{"name": "seed", "default": 0},
			{"name": "tolerance", "required": false, "default": 0.25},
			{"name": "label"}
		]},
		{"name": "hello", "streaming": true}
	]`))
	if err != nil {
		t.Fatal(err)
	}

	parameters, present := dm.Parameters("montecarlo")
	if !present {
		t.Fatal("missing directive")
	}
	zero, quarter := 0.0, 0.25
	expected := []Parameter{
		{Name: "iterations", Required: true},
		{Name: "seed", Default: &zero},
		{Name: "tolerance", Default: &quarter},
		{Name: "label"},
	}
	if !reflect.DeepEqual(parameters, expected) {
		t.Errorf("unexpected parameters %+v", parameters)
	}

	if parameters, present = dm.Parameters("hello"); !present {
		t.Error("missing directive")
	} else if parameters == nil || len(parameters) != 0 {
		t.Errorf("expected empty parameters, got %#v", parameters)
	}

	if _, present = dm.Parameters("unknown"); present {
		t.Error("unexpected directive")
	}
}

func Test_thatInvalidDirectivesFail(t *testing.T) {
	cases := map[string]string{
		"duplicate name":          `[{"name": "hello", "streaming": true}, {"name": "hello", "streaming": false}]`,
		"missing name":            `[{"streaming": true}]`,
		"old syntax":              `[{"name": "hello", "batch": {"files": false}}]`,
		"not a list":              `{"name": "hello", "streaming": true}`,
		"duplicate parameter":     `[{"name": "hello", "parameters": [{"name": "a"}, {"name": "a", "required": true}]}]`,
		"unnamed parameter":       `[{"name": "hello", "parameters": [{"required": true}]}]`,
		"unknown parameter field": `[{"name": "hello", "parameters": [{"name": "a", "type": "float"}]}]`,
		"non-numeric default":     `[{"name": "hello", "parameters": [{"name": "a", "default": "1.0"}]}]`,
		"non-boolean required":    `[{"name": "hello", "parameters": [{"name": "a", "required": "yes"}]}]`,
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
