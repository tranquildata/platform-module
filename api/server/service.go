/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/tranquildata/platform-module/config"
	"github.com/tranquildata/platform-module/module"
)

const InputFilePath = "/moduleio/input"
const OutputDirectoryPath = "/moduleio/output"

type ServiceInfo struct {
	Directive string `json:"directive"`
	Batch     bool   `json:"batch"`
}

type APIService struct {
	runtimeConfig   *config.RuntimeConfig
	batch           bool
	fileIO          bool
	activeModule    module.Module
	moduleChannel   chan error
	shutdownChannel chan any
}

func Setup(runtimeConfig *config.RuntimeConfig, batch bool, fileIO bool) *APIService {
	return &APIService{
		runtimeConfig:   runtimeConfig,
		batch:           batch,
		fileIO:          fileIO,
		moduleChannel:   make(chan error),
		shutdownChannel: make(chan any),
	}
}

// Serve attempts to open the given port, initializes API endpoints, and
// blocks until either module startup fails or a module successfully runs
// to completion. Typically, this is used as the sustaining endpoint. If
// the API service isn't invoked within five mintues of startup then it
// will shutdown to ensure that resources don't leak.
func (apis *APIService) Serve(port uint16) error {
	// setup and serve the HTTP API endpoint
	mux := http.NewServeMux()
	apis.registerHTTPEndpoints(mux)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}
	go httpServer.ListenAndServe()

	// wait for either API input or five minutes to pass
	timeout := time.NewTicker(time.Minute * 5)
	select {
	case err := <-apis.moduleChannel:
		// check that there was no error in starting-up the module
		if err != nil {
			return err
		}

	case <-timeout.C:
		// we waited too long for API interaction so clean up
		return fmt.Errorf("no interaction after timeout window; exiting service")
	}

	// the module was successfully started, so wait for shutdown notification
	<-apis.shutdownChannel

	// finish by running a graceful shutdown of the web server, which will block
	// until any outstanding exchanges wrap up
	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(time.Second*30))
	defer cancel()
	return httpServer.Shutdown(ctx)
}

func (apis *APIService) registerHTTPEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("HEAD /", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("OPTIONS /", apis.serviceInfo)
	mux.HandleFunc("PUT /", apis.inputData)
}

func (apis *APIService) serviceInfo(w http.ResponseWriter, r *http.Request) {
	if responseBytes, err := json.Marshal(apis.serviceMap()); err != nil {
		http.Error(w, "failed to marshal service info"+err.Error(), http.StatusInternalServerError)
	} else {
		w.Write(responseBytes) //nolint:errcheck
	}
}

func (apis *APIService) serviceMap() *ServiceInfo {
	return &ServiceInfo{
		Directive: apis.runtimeConfig.Directive,
		Batch:     apis.batch,
	}
}

func (apis *APIService) inputData(w http.ResponseWriter, r *http.Request) {
	// NOTE: we're currently only supporting batch oprerations, so this routine will
	// always trigger shutdown, but that will need to be re-visited when we add
	// support for interactive modules
	defer func() {
		apis.shutdownChannel <- nil
	}()

	if bodyBytes, err := io.ReadAll(r.Body); err != nil {
		http.Error(w, "failed to read input: "+err.Error(), http.StatusBadRequest)
		return
	} else if err = apis.handleInput(append(bodyBytes, '\n')); err != nil {
		http.Error(w, "failed to provide input to module: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if responseMap, err := apis.handleFullOutput(); err != nil {
		http.Error(w, "failed to get output from module: "+err.Error(), http.StatusInternalServerError)
		return
	} else if responseBytes, err := json.Marshal(responseMap); err != nil {
		http.Error(w, "failed to encode module output: "+err.Error(), http.StatusInternalServerError)
		return
	} else {
		w.Write(responseBytes) //nolint:errcheck
	}
}

// handleInput routes data to the module. If the module hasn't been started yet
// then this routine will start the module. If the module expects file input
// then this routine will write the input to the well-known input file before
// starting the module.
func (apis *APIService) handleInput(inputBytes []byte) error {
	// if there is no running module, start it now
	if apis.activeModule == nil {
		// if we're running in FileIO mode then write the file first
		if apis.fileIO {
			if err := os.WriteFile(InputFilePath, inputBytes, 0444); err != nil {
				return err
			}
		}
		if err := apis.startModule(); err != nil {
			return err
		}
	}

	if !apis.fileIO {
		return apis.activeModule.Write(inputBytes)
	}
	return nil
}

// startModule attempts to start the module via the wrapper script, communicating
// the channel listener either the new module or failure
func (apis *APIService) startModule() error {
	activeModule, err := module.Start(apis.runtimeConfig.Environment())
	apis.activeModule = activeModule
	apis.moduleChannel <- err
	return err
}

// handleFullOutput retrieves the final data output from the module. This routine will
// Wait() on the module before returning.
func (apis *APIService) handleFullOutput() (map[string][]byte, error) {
	outputMap := map[string][]byte{}

	if apis.fileIO {
		// wait for the module to complete to ensure all files are written
		if err := apis.activeModule.WaitForStop(); err != nil {
			return nil, err
		}

		// marshal up all the file contents into a map
		if files, err := os.ReadDir(OutputDirectoryPath); err != nil {
			return nil, err
		} else {
			for _, entry := range files {
				if !entry.IsDir() {
					if fileBytes, err := os.ReadFile(entry.Name()); err == nil {
						outputMap[entry.Name()] = fileBytes
					}
				}
			}
		}
	} else {
		// read all output until there's nothing left, and put that into a single entry
		// with an empty name to signal this was standard-out not a named file
		var buffer bytes.Buffer
		if _, err := apis.activeModule.ReadFully(&buffer); err != nil {
			return nil, err
		} else {
			outputMap[""] = buffer.Bytes()
		}
		// for parity with the fileIO version, wait for the module to terminate, which
		// should be instant since stdout is already closed
		if err := apis.activeModule.WaitForStop(); err != nil {
			return nil, err
		}
	}

	return outputMap, nil
}
