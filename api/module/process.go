/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package module

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
)

// ModuleWrapper is the script that wraps the bundled module.
const ModuleWrapper = "/usr/local/bin/tqd-module-wrapper"

type Module interface {
	// WaitForStop blocks until the module terminates.
	WaitForStop() error
	// Read reads from the module via its standard-out pipe.
	Read(buffer []byte) (int, error)
	// ReadFully reads from the module via its stanard-out pipe until EOF is reached.
	ReadFully(buffer *bytes.Buffer) (int64, error)
	// Write writes to the module via its standard-in pipe.
	Write(message []byte) error
}

type module struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output io.ReadCloser
}

// Start attempts to reolve & start the bundled module via the ModuleWrapper script.
func Start(environment map[string]string) (Module, error) {
	cmd := exec.Command(ModuleWrapper)
	cmd.Env = make([]string, len(environment))[0:0]
	for k, v := range environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	cmdInput, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to module stdin: %s", err.Error())
	}

	cmdOutput, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to module stdout: %s\n", err.Error())
	}

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start module: %s\n", err.Error())
	}

	return &module{
		cmd:    cmd,
		input:  cmdInput,
		output: cmdOutput,
	}, nil
}

func (m *module) WaitForStop() error {
	return m.cmd.Wait()
}

func (m *module) Read(buffer []byte) (int, error) {
	return m.output.Read(buffer)
}

func (m *module) ReadFully(buffer *bytes.Buffer) (int64, error) {
	return io.Copy(buffer, m.output)
}

func (m *module) Write(message []byte) error {
	if count, err := m.input.Write(message); err != nil {
		return err
	} else if len(message) != count {
		return fmt.Errorf("failed to write full message")
	}
	return nil
}
