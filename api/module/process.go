/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package module

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// ModuleWrapper is the script that wraps the bundled module.
const ModuleWrapper = "/usr/local/bin/tqd-module-wrapper"

type Module interface {
	// WaitForStop blocks until the module terminates. For streaming modules, all
	// reads from standard-out must complete before this is called.
	WaitForStop() error
	// Read reads from the module via its standard-out pipe (streaming only).
	Read(buffer []byte) (int, error)
	// Write writes to the module via its standard-in pipe (streaming only).
	Write(message []byte) error
	// CloseInput closes the module's standard-in pipe, signaling EOF (streaming only).
	CloseInput() error
}

type module struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output io.ReadCloser
}

// Start attempts to reolve & start the bundled module via the ModuleWrapper script.
// Streaming modules are connected via standard IO pipes. Batch modules do their IO
// through files, so their standard-out and standard-err are passed through to the
// API service's own for logging.
func Start(environment map[string]string, streaming bool) (Module, error) {
	cmd := exec.Command(ModuleWrapper)
	cmd.Env = make([]string, len(environment))[0:0]
	for k, v := range environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Stderr = os.Stderr

	m := &module{
		cmd: cmd,
	}

	if streaming {
		cmdInput, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to connect to module stdin: %s", err.Error())
		}

		cmdOutput, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to connect to module stdout: %s", err.Error())
		}

		m.input = cmdInput
		m.output = cmdOutput
	} else {
		cmd.Stdout = os.Stdout
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start module: %s", err.Error())
	}

	return m, nil
}

func (m *module) WaitForStop() error {
	return m.cmd.Wait()
}

func (m *module) Read(buffer []byte) (int, error) {
	if m.output == nil {
		return 0, fmt.Errorf("batch modules do not support standard-out")
	}
	return m.output.Read(buffer)
}

func (m *module) Write(message []byte) error {
	if m.input == nil {
		return fmt.Errorf("batch modules do not support standard-in")
	}
	if count, err := m.input.Write(message); err != nil {
		return err
	} else if len(message) != count {
		return fmt.Errorf("failed to write full message")
	}
	return nil
}

func (m *module) CloseInput() error {
	if m.input == nil {
		return fmt.Errorf("batch modules do not support standard-in")
	}
	return m.input.Close()
}
