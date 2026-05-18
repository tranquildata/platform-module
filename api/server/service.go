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
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tranquildata/platform-module/config"
	"github.com/tranquildata/platform-module/module"
)

const InputFilePath = "/moduleio/input"
const OutputDirectoryPath = "/moduleio/output"
const RunIdParamName = "runId"

type ServiceInfo struct {
	Directive string `json:"directive"`
	Batch     bool   `json:"batch"`
}

type APIService struct {
	runtimeConfig *config.RuntimeConfig
	batch         bool
	fileIO        bool
	activeModule  module.Module

	waitTimeout     time.Duration
	runID           atomic.Value
	logger          *slog.Logger
	moduleChannel   chan error
	shutdownChannel chan any
	outputChannel   chan *output
}

type output struct {
	outputFiles map[string][]byte
	err         error
}

func Setup(runtimeConfig *config.RuntimeConfig, batch bool, fileIO bool) *APIService {
	logger := slog.New(slog.NewTextHandler(
		os.Stdout,
		&slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
	return &APIService{
		runtimeConfig:   runtimeConfig,
		batch:           batch,
		fileIO:          fileIO,
		logger:          logger,
		waitTimeout:     1 * time.Second,
		moduleChannel:   make(chan error),
		shutdownChannel: make(chan any),
		outputChannel:   make(chan *output),
	}
}

type panicWrapper struct {
	handler http.Handler
}

func (pw *panicWrapper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panicWrapper ServeHTTP Recovered from a panic", "reason", r, "stack", string(debug.Stack()))
		}
	}()
	pw.handler.ServeHTTP(w, r)
}

func listenAndServe(httpServer *http.Server) {
	err := httpServer.ListenAndServe()
	if err != nil {
		slog.Error("error listening", "error", err)
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
	pw := &panicWrapper{
		handler: mux,
	}
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: pw,
	}
	go listenAndServe(httpServer)

	// wait for either API input or five minutes to pass
	timeout := time.NewTicker(time.Minute * 20)
	select {
	case err := <-apis.moduleChannel:
		// check that there was no error in starting-up the module
		if err != nil {
			apis.logger.Error("error starting up the module", "error", err)
			return err
		}
		apis.logger.Debug("no error starting up the module")

	case <-timeout.C:
		// we waited too long for API interaction so clean up
		apis.logger.Error("interaction timeout")
		return fmt.Errorf("no interaction after timeout window; exiting service")
	}

	apis.logger.Debug("waiting for shutdown signal")
	// the module was successfully started, so wait for shutdown notification
	<-apis.shutdownChannel

	apis.logger.Debug("got shutdown signal")
	// finish by running a graceful shutdown of the web server, which will block
	// until any outstanding exchanges wrap up
	ctx, cancel := context.WithDeadline(context.TODO(), time.Now().Add(time.Second*30))
	defer cancel()
	return httpServer.Shutdown(ctx)
}

func (apis *APIService) registerHTTPEndpoints(mux *http.ServeMux) {
	mux.HandleFunc("HEAD /", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("OPTIONS /", apis.serviceInfo)
	mux.HandleFunc("PUT /", apis.inputData) //synchronous, waits for output
	mux.HandleFunc("PUT /async", apis.asyncInputData)
	mux.HandleFunc("GET /async", apis.asyncGetOutput)
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

func (apis *APIService) asyncInputData(w http.ResponseWriter, r *http.Request) {
	var runId string
	if runIds, OK := r.URL.Query()[RunIdParamName]; OK && len(runIds) > 0 {
		runId = runIds[0]
	}
	if runningId := apis.runID.Load(); runningId != nil {
		if asStr, strOK := runningId.(string); strOK && len(asStr) > 0 {
			//already running something, we need to fail
			http.Error(w, fmt.Sprintf("already running a job with ID %s", asStr), http.StatusConflict)
			return
		}
	}

	if bodyBytes, err := io.ReadAll(r.Body); err != nil {
		http.Error(w, "failed to read input: "+err.Error(), http.StatusBadRequest)
		return
	} else if err = apis.handleInput(append(bodyBytes, '\n')); err != nil {
		http.Error(w, "failed to provide input to module: "+err.Error(), http.StatusInternalServerError)
		return
	}
	apis.runID.Store(runId)

	//kick off goroutine to wait for output
	go apis.handleOutputAsync(apis.outputChannel)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(runId))
}

func (apis *APIService) asyncGetOutput(w http.ResponseWriter, r *http.Request) {
	var runId string
	if runIds, OK := r.URL.Query()[RunIdParamName]; OK && len(runIds) > 0 {
		runId = runIds[0]
	}
	if runningId := apis.runID.Load(); runningId != nil {
		if asStr, strOK := runningId.(string); strOK && len(asStr) > 0 {
			if asStr != runId {
				http.Error(w, fmt.Sprintf("invalid running id %s", runId), http.StatusBadRequest)
				return
			}
		}
	} else {
		http.Error(w, "no job started", http.StatusBadRequest)
		return
	}

	var out *output
	select {
	case out = <-apis.outputChannel:
		//pass
	case <-time.After(apis.waitTimeout):
		http.Error(w, "timed out waiting for output", http.StatusGatewayTimeout)
		return
	}

	if out.err != nil {
		http.Error(w, out.err.Error(), http.StatusInternalServerError)
		return
	} else if len(out.outputFiles) > 0 {
		if responseBytes, err := json.Marshal(out.outputFiles); err != nil {
			http.Error(w, "failed to encode module output: "+err.Error(), http.StatusInternalServerError)
			return
		} else {
			w.Write(responseBytes) //nolint:errcheck
		}
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (apis *APIService) inputData(w http.ResponseWriter, r *http.Request) {
	// NOTE: we're currently only supporting batch oprerations, so this routine will
	// always trigger shutdown, but that will need to be re-visited when we add
	// support for interactive modules
	defer func() {
		//this will shut things down messily. Any buffered I/O will be lost, so we need to be sure to
		// flush anything before shutting down
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
			if err := os.WriteFile(InputFilePath, inputBytes, 0666); err != nil {
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
	if err != nil {
		apis.logger.Error("module started with error", "error", err)
	} else {
		apis.logger.Debug("module started")
	}
	apis.activeModule = activeModule
	apis.moduleChannel <- err
	return err
}

func (apis *APIService) handleOutputAsync(outputCh chan *output) {
	outputFiles, err := apis.handleFullOutput()
	out := &output{
		outputFiles: outputFiles,
		err:         err,
	}
	outputCh <- out
}

// handleFullOutput retrieves the final data output from the module. This routine will
// Wait() on the module before returning.
func (apis *APIService) handleFullOutput() (map[string][]byte, error) {
	outputMap := map[string][]byte{}

	if apis.fileIO {
		apis.logger.Debug("using fileIO")
		// wait for the module to complete to ensure all files are written
		if err := apis.activeModule.WaitForStop(); err != nil {
			apis.logger.Error("waitForStop error", "error", err)
			buffer := bytes.Buffer{}
			if _, readErr := apis.activeModule.ReadFully(&buffer); readErr != nil {
				apis.logger.Error("error reading command output", "error", readErr)
			}
			return nil, err
		}

		// marshal up all the file contents into a map
		if files, err := os.ReadDir(OutputDirectoryPath); err != nil {
			apis.logger.Error("readDir error", "error", err)
			return nil, err
		} else {
			names := make([]string, len(files))[0:0]
			for _, entry := range files {
				if !entry.IsDir() {
					if fileBytes, err := os.ReadFile(fmt.Sprintf("%s/%s", OutputDirectoryPath, entry.Name())); err == nil {
						outputMap[entry.Name()] = fileBytes
					}
					names = append(names, entry.Name())
				} else {
					names = append(names, fmt.Sprintf("%s/", entry.Name()))
				}
			}
			if apis.logger.Enabled(context.Background(), slog.LevelDebug) {
				apis.logger.Debug("output files", "fileList", strings.Join(names, ","))
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
