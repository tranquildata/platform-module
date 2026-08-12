/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package server

import (
	"encoding/json"
	"os"
	"path"
	"reflect"
	"testing"
)

func Test_thatASingleOutputWorks(t *testing.T) {
	outs := &output{
		outputFilenames:   []string{"run01/file01.csv", "run02/file02.csv", "run02/file03.csv"},
		outputCursor:      0,
		maximumOutputSize: 512,
	}
	resp := &OutputResponse{}
	responseBytes, err := outs.responseBytes("testresources")
	if err != nil {
		t.Fatal(err)
	} else if err = json.Unmarshal(responseBytes, resp); err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]bool, len(outs.outputFilenames))
	for _, nextFilename := range outs.outputFilenames {
		expected[nextFilename] = true
	}
	for k, _ := range resp.Outputs {
		if _, present := expected[k]; !present {
			t.Errorf("unexpected output: %s", k)
		} else {
			delete(expected, k)
		}
	}
	if len(expected) > 0 {
		t.Errorf("missing outputs: %v", expected)
	}
	if resp.CursorOffset != 0 {
		t.Errorf("bad cursor offset %d", resp.CursorOffset)
	}
}

func Test_thatPagingWorks(t *testing.T) {
	outs := &output{
		outputFilenames:   []string{"run01/file01.csv", "run02/file02.csv", "run02/file03.csv"},
		outputCursor:      0,
		maximumOutputSize: 1,
	}
	var resp *OutputResponse
	for idx, expectedFilename := range outs.outputFilenames {
		resp = &OutputResponse{}
		responseBytes, err := outs.responseBytes("testresources")
		if err != nil {
			t.Fatal(err)
		} else if err = json.Unmarshal(responseBytes, resp); err != nil {
			t.Fatal(err)
		}
		if l := len(resp.Outputs); l != 1 {
			t.Errorf("bad response output size %d", l)
		} else if _, present := resp.Outputs[expectedFilename]; !present {
			t.Errorf("missing expected file %s", expectedFilename)
		}
		if resp.CursorOffset != (idx+1)%len(outs.outputFilenames) {
			t.Errorf("bad cursor offset %d", resp.CursorOffset)
		}
	}
	//make sure that the cursor offset is 0 at the end
	if resp.CursorOffset != 0 {
		t.Errorf("bad ending cursor offset %d != 0", resp.CursorOffset)
	}
	//make sure that it resets to the beginning
	resp = &OutputResponse{}
	responseBytes, err := outs.responseBytes("testresources")
	if err != nil {
		t.Fatal(err)
	} else if err = json.Unmarshal(responseBytes, resp); err != nil {
		t.Fatal(err)
	} else if resp.CursorOffset != 1 {
		t.Errorf("bad cursor offset %d", resp.CursorOffset)
	} else if _, present := resp.Outputs[outs.outputFilenames[0]]; !present {
		t.Errorf("missing expected file")
	}
}

func Test_thatPagingWorksWithCoPrimeIndices(t *testing.T) {
	outs := &output{
		outputFilenames:   []string{"run02/file03.csv", "run01/file01.csv", "run02/file02.csv"}, //277 bytes, 78 bytes, 60 bytes
		outputCursor:      0,
		maximumOutputSize: 200,
	}
	var resp *OutputResponse
	remaining := make(map[string]bool, len(outs.outputFilenames))
	for _, nextOutput := range outs.outputFilenames {
		remaining[nextOutput] = true
	}
	var offset int = -1
	for offset != 0 {
		resp = &OutputResponse{}
		responseBytes, err := outs.responseBytes("testresources")
		if err != nil {
			t.Fatal(err)
		} else if err = json.Unmarshal(responseBytes, resp); err != nil {
			t.Fatal(err)
		}
		if l := len(resp.Outputs); l == 0 {
			t.Errorf("empty outputs %d", l)
		}
		for nextOutput, _ := range resp.Outputs {
			if _, present := remaining[nextOutput]; !present {
				t.Errorf("unexpected file %s", nextOutput)
			} else {
				delete(remaining, nextOutput)
			}
		}
		if resp.CursorOffset != 1 && resp.CursorOffset != 0 {
			//first call should return 1 file, the second call should return the rest
			t.Errorf("bad cursor offset %d", resp.CursorOffset)
		}
		offset = resp.CursorOffset
	}
	if len(remaining) > 0 {
		t.Errorf("missing expected files %v", remaining)
	}
	//make sure that the cursor offset is 0 at the end
	if resp.CursorOffset != 0 {
		t.Errorf("bad ending cursor offset %d != 0", resp.CursorOffset)
	}
	//make sure that it resets to the beginning and returns the first file over again
	resp = &OutputResponse{}
	responseBytes, err := outs.responseBytes("testresources")
	if err != nil {
		t.Fatal(err)
	} else if err = json.Unmarshal(responseBytes, resp); err != nil {
		t.Fatal(err)
	} else if resp.CursorOffset != 1 {
		t.Errorf("bad cursor offset %d", resp.CursorOffset)
	} else if _, present := resp.Outputs[outs.outputFilenames[0]]; !present {
		t.Errorf("missing expected file")
	}
}

func Test_incrementalFileUploading(t *testing.T) {
	files := map[string][]byte{
		"input01": []byte("first file"),
		"input02": []byte("second file"),
		"input03": []byte("third file contents"),
	}
	inputDir := t.TempDir()
	for name, data := range files {
		input := map[string][]byte{
			name: data,
		}
		bytez, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if err = handleFileInput(bytez, inputDir); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		t.Fatal(err)
	}
	readFiles := make(map[string][]byte, len(entries))
	for _, nextEntry := range entries {
		data, err := os.ReadFile(path.Join(inputDir, nextEntry.Name()))
		if err != nil {
			t.Errorf("error reading file %s: %s", nextEntry.Name(), err.Error())
		} else {
			readFiles[nextEntry.Name()] = data
		}
	}
	if !reflect.DeepEqual(files, readFiles) {
		t.Errorf("unexpected input files %v != %v", readFiles, files)
	}
}

func Test_allAtOnceFileUploading(t *testing.T) {
	files := map[string][]byte{
		"input01": []byte("first file"),
		"input02": []byte("second file"),
		"input03": []byte("third file contents"),
	}
	inputDir := t.TempDir()
	bytez, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	if err = handleFileInput(bytez, inputDir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		t.Fatal(err)
	}
	readFiles := make(map[string][]byte, len(entries))
	for _, nextEntry := range entries {
		data, err := os.ReadFile(path.Join(inputDir, nextEntry.Name()))
		if err != nil {
			t.Errorf("error reading file %s: %s", nextEntry.Name(), err.Error())
		} else {
			readFiles[nextEntry.Name()] = data
		}
	}
	if !reflect.DeepEqual(files, readFiles) {
		t.Errorf("unexpected input files %v != %v", readFiles, files)
	}
}
