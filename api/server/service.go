/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tranquildata/platform-module/config"
	"github.com/tranquildata/platform-module/module"
)

const InputDirectoryPath = "/moduleio/input"
const OutputDirectoryPath = "/moduleio/output"
const RunIdParamName = "runId"
const UploadOnlyParamName = "uploadOnly"

const DefaultMaximumOutputSize = 64 * 1024 * 1024 //64 MB
const DefaultCollectionTimeout = 20 * time.Minute

var errModuleRunning = fmt.Errorf("module is already running; batch input must be staged before the module starts")
var errInputClosed = fmt.Errorf("module input has already been closed")

type ServiceInfo struct {
	Directive string `json:"directive"`
	Streaming bool   `json:"streaming"`
}

type APIService struct {
	runtimeConfig     *config.RuntimeConfig
	streaming         bool
	maximumOutputSize uint64
	inputDirectory    string
	outputDirectory   string
	startFn           func(map[string]string, bool) (module.Module, error)

	moduleLock   sync.Mutex //guards module start-up and input
	activeModule module.Module
	inputClosed  bool    //streaming case only, set once the module's standard-in is closed
	stream       *stream //streaming case only, set when the module starts
	shutdownOnce sync.Once

	waitTimeout       time.Duration
	collectionTimeout time.Duration //how long async output may go uncollected after the module terminates
	collectionLock    sync.Mutex
	collectionTimer   *time.Timer
	runID             atomic.Value
	output            atomic.Value

	logger          *slog.Logger
	moduleChannel   chan error
	shutdownChannel chan any
	outputChannel   chan *output
}

type output struct {
	//batch case
	outputFilenames   []string //the list of output filenames
	cursorLock        sync.Mutex
	outputCursor      int    //the spot in outputFilenames where the last output GET stopped (0 means no GET as yet, len(outputFilenames) means all output was sent)
	maximumOutputSize uint64 //the size at which to end an output page, 0 means no paging
	allSent           bool   //set once the final page of output has been returned

	//streaming case
	outputBytes []byte

	//both cases
	err error
}

type OutputResponse struct {
	Outputs      map[string][]byte `json:"outputs"`      //the set of output files, for streaming runs there will be one entry with the key of ""
	CursorOffset int               `json:"cursorOffset"` //the offset at which to start the next output page, 0 means all output has been returned
}

func Setup(runtimeConfig *config.RuntimeConfig, streaming bool) *APIService {
	logger := slog.New(slog.NewTextHandler(
		os.Stdout,
		&slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
	return &APIService{
		runtimeConfig:     runtimeConfig,
		streaming:         streaming,
		logger:            logger,
		waitTimeout:       1 * time.Second,
		collectionTimeout: DefaultCollectionTimeout,
		maximumOutputSize: DefaultMaximumOutputSize,
		inputDirectory:    InputDirectoryPath,
		outputDirectory:   OutputDirectoryPath,
		startFn:           module.Start,
		moduleChannel:     make(chan error, 1),
		shutdownChannel:   make(chan any, 1),
		outputChannel:     make(chan *output),
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
	mux.HandleFunc("HEAD /{$}", apis.status)
	mux.HandleFunc("OPTIONS /", apis.serviceInfo)
	mux.HandleFunc("PUT /", apis.inputData)
	mux.HandleFunc("GET /{$}", apis.streamOutput)      //streaming only, collects any output produced since the last call
	mux.HandleFunc("DELETE /{$}", apis.closeInput)     //streaming only, closes standard-in and collects any output
	mux.HandleFunc("OPTIONS /async", apis.serviceInfo) //synchronous, waits for output
	mux.HandleFunc("PUT /async", apis.asyncInputData)
	mux.HandleFunc("GET /async", apis.asyncGetOutput)
}

func (apis *APIService) status(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
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
		Streaming: apis.streaming,
	}
}

func (apis *APIService) asyncInputData(w http.ResponseWriter, r *http.Request) {
	if apis.streaming {
		http.Error(w, "async input is only supported for batch directives", http.StatusBadRequest)
		return
	}
	var runId string
	if runIds, OK := r.URL.Query()[RunIdParamName]; OK && len(runIds) > 0 {
		runId = runIds[0]
	} else {
		http.Error(w, fmt.Sprintf("missing %s query parameter", RunIdParamName), http.StatusBadRequest)
		return
	}
	uploadOnly := false
	if uploadParams, OK := r.URL.Query()[UploadOnlyParamName]; OK && len(uploadParams) > 0 {
		uploadOnly = true
	}
	if runningId := apis.runID.Load(); runningId != nil {
		if asStr, strOK := runningId.(string); strOK && len(asStr) > 0 && runId != runningId {
			//already running something, we need to fail
			http.Error(w, fmt.Sprintf("already running a job with ID %s", asStr), http.StatusConflict)
			return
		}
	}

	if bodyBytes, err := io.ReadAll(r.Body); err != nil {
		http.Error(w, "failed to read input: "+err.Error(), http.StatusBadRequest)
		return
	} else if err = apis.handleInput(append(bodyBytes, '\n'), !uploadOnly); err != nil {
		http.Error(w, "failed to provide input to module: "+err.Error(), inputErrorStatus(err))
		return
	}
	apis.runID.Store(runId)

	if !uploadOnly {
		//kick off goroutine to wait for output
		go apis.handleOutputAsync(apis.outputChannel)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(runId))
}

func (apis *APIService) asyncGetOutput(w http.ResponseWriter, r *http.Request) {
	if apis.streaming {
		http.Error(w, "async output is only supported for batch directives", http.StatusBadRequest)
		return
	}
	var runId string
	if runIds, OK := r.URL.Query()[RunIdParamName]; OK && len(runIds) > 0 {
		runId = runIds[0]
	} else {
		http.Error(w, fmt.Sprintf("missing %s query parameter", RunIdParamName), http.StatusBadRequest)
		return
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
	if savedOutput := apis.output.Load(); savedOutput != nil {
		if outStruct, outputOK := savedOutput.(*output); outputOK {
			out = outStruct
		}
	}
	if out == nil {
		select {
		case out = <-apis.outputChannel:
			//pass
		case <-time.After(apis.waitTimeout):
			http.Error(w, "timed out waiting for output", http.StatusGatewayTimeout)
			return
		}
	}
	if out == nil {
		//some issue caused a nil to be written to the channel
		out = &output{
			err: fmt.Errorf("nil output"),
		}
	}
	apis.output.Store(out)
	apis.resetCollectionTimer()

	// the module has terminated, so once its error, its last page of output, or the
	// fact that it had no output has been returned, the run is complete
	if out.err != nil {
		defer apis.requestShutdown()
		http.Error(w, out.err.Error(), http.StatusInternalServerError)
	} else if len(out.outputFilenames) > 0 {
		//output is present, fetch the next page
		if responseBytes, err := out.responseBytes(apis.outputDirectory); err != nil {
			http.Error(w, "failed to encode module output: "+err.Error(), http.StatusInternalServerError)
		} else {
			if out.finished() {
				defer apis.requestShutdown()
			}
			w.Write(responseBytes) //nolint:errcheck
		}
	} else {
		//the module terminated without writing any output files
		defer apis.requestShutdown()
		w.WriteHeader(http.StatusNoContent)
	}
}

// startCollectionTimer starts the window in which output must be collected
// after the module terminates. If it expires, shutdown is requested so that an
// abandoned run doesn't keep the container alive.
func (apis *APIService) startCollectionTimer() {
	apis.collectionLock.Lock()
	defer apis.collectionLock.Unlock()
	if apis.collectionTimer != nil {
		return
	}
	apis.collectionTimer = time.AfterFunc(apis.collectionTimeout, func() {
		apis.logger.Error("module output was not collected before timeout; shutting down")
		apis.requestShutdown()
	})
}

// resetCollectionTimer restarts the collection window after each output request.
// It has no effect until the module has terminated.
func (apis *APIService) resetCollectionTimer() {
	apis.collectionLock.Lock()
	defer apis.collectionLock.Unlock()
	if apis.collectionTimer != nil {
		apis.collectionTimer.Reset(apis.collectionTimeout)
	}
}

// inputData provides input to the module. For batch directives the input is a set of
// files that are staged before the module is started, and the call blocks until the
// module terminates and then returns the output files. For streaming directives the
// input is written to the module's standard-in, and any output produced on standard-out
// is returned. In both cases the service shuts down once the module has terminated
// and all of its output has been returned.
func (apis *APIService) inputData(w http.ResponseWriter, r *http.Request) {
	if apis.streaming {
		apis.streamInputData(w, r)
		return
	}

	// batch runs are complete after a single call, so this routine always triggers shutdown
	defer apis.requestShutdown()

	if bodyBytes, err := io.ReadAll(r.Body); err != nil {
		http.Error(w, "failed to read input: "+err.Error(), http.StatusBadRequest)
		return
	} else if err = apis.handleInput(append(bodyBytes, '\n'), true); err != nil {
		http.Error(w, "failed to provide input to module: "+err.Error(), inputErrorStatus(err))
		return
	}

	outs := apis.handleOutput()
	// this is the only chance to return output before shutdown, so return all of it
	outs.maximumOutputSize = 0
	if outs.err != nil {
		http.Error(w, "failed to get output from module: "+outs.err.Error(), http.StatusInternalServerError)
		return
	} else if responseBytes, err := outs.responseBytes(apis.outputDirectory); err != nil {
		http.Error(w, "failed to encode module output: "+err.Error(), http.StatusInternalServerError)
		return
	} else {
		w.Write(responseBytes) //nolint:errcheck
	}
}

func (apis *APIService) streamInputData(w http.ResponseWriter, r *http.Request) {
	if bodyBytes, err := io.ReadAll(r.Body); err != nil {
		http.Error(w, "failed to read input: "+err.Error(), http.StatusBadRequest)
		return
	} else if err = apis.handleInput(append(bodyBytes, '\n'), true); err != nil {
		http.Error(w, "failed to provide input to module: "+err.Error(), inputErrorStatus(err))
		return
	}
	apis.writeStreamOutput(w)
}

// streamOutput returns any output the streaming module has produced since the
// last call, without providing new input.
func (apis *APIService) streamOutput(w http.ResponseWriter, r *http.Request) {
	if !apis.streaming {
		http.Error(w, "output without input is only supported for streaming directives", http.StatusBadRequest)
		return
	}
	apis.moduleLock.Lock()
	s := apis.stream
	apis.moduleLock.Unlock()
	if s == nil {
		http.Error(w, "module has not been started", http.StatusBadRequest)
		return
	}
	apis.writeStreamOutput(w)
}

// writeStreamOutput waits (up to the wait timeout) for the streaming module to
// produce output, and returns whatever is available. Once the module has terminated
// and all of its output has been returned, shutdown is requested.
func (apis *APIService) writeStreamOutput(w http.ResponseWriter) {
	outs, finished := apis.stream.collect(apis.waitTimeout)
	apis.resetCollectionTimer()
	if finished {
		apis.requestShutdown()
	}
	if outs.err != nil && len(outs.outputBytes) == 0 {
		http.Error(w, "module failed: "+outs.err.Error(), http.StatusInternalServerError)
	} else if responseBytes, err := outs.responseBytes(apis.outputDirectory); err != nil {
		http.Error(w, "failed to encode module output: "+err.Error(), http.StatusInternalServerError)
	} else {
		w.Write(responseBytes) //nolint:errcheck
	}
}

// closeInput closes the streaming module's standard-in, signaling that no more
// input will be provided, and returns any output as streamOutput does. Closing
// input that is already closed has no further effect.
func (apis *APIService) closeInput(w http.ResponseWriter, r *http.Request) {
	if !apis.streaming {
		http.Error(w, "closing input is only supported for streaming directives", http.StatusBadRequest)
		return
	}
	apis.moduleLock.Lock()
	if apis.activeModule == nil {
		apis.moduleLock.Unlock()
		http.Error(w, "module has not been started", http.StatusBadRequest)
		return
	}
	var err error
	if !apis.inputClosed {
		apis.inputClosed = true
		err = apis.activeModule.CloseInput()
	}
	apis.moduleLock.Unlock()
	if err != nil {
		http.Error(w, "failed to close module input: "+err.Error(), http.StatusInternalServerError)
		return
	}
	apis.writeStreamOutput(w)
}

func inputErrorStatus(err error) int {
	if err == errModuleRunning || err == errInputClosed {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// requestShutdown signals Serve to shut down the service. It never blocks, and
// only the first request has any effect.
func (apis *APIService) requestShutdown() {
	apis.shutdownOnce.Do(func() {
		apis.shutdownChannel <- nil
	})
}

func (o *output) responseBytes(outputDirectory string) ([]byte, error) {
	resp := &OutputResponse{
		Outputs: make(map[string][]byte, len(o.outputFilenames)+1),
	}
	var err error
	if o.outputFilenames == nil {
		resp.Outputs[""] = o.outputBytes
	} else if resp.Outputs, resp.CursorOffset, err = o.fileMap(outputDirectory); err != nil {
		return nil, err
	}
	return json.Marshal(resp)
}

func (o *output) fileMap(outputDirectory string) (map[string][]byte, int, error) {
	outputs := make(map[string][]byte, len(o.outputFilenames))
	o.cursorLock.Lock()
	defer o.cursorLock.Unlock()
	currentSize := 0
	idx := o.outputCursor
	for ; idx < len(o.outputFilenames); idx++ {
		filename := o.outputFilenames[idx]
		if fileBytes, err := os.ReadFile(path.Join(outputDirectory, filename)); err != nil {
			return nil, 0, err
		} else {
			outputs[filename] = fileBytes
			currentSize += len(fileBytes)
		}
		if o.maximumOutputSize > 0 && currentSize >= int(o.maximumOutputSize) {
			break
		}
	}
	if idx >= len(o.outputFilenames) {
		o.outputCursor = 0
	} else {
		o.outputCursor = (idx + 1) % len(o.outputFilenames)
	}
	if o.outputCursor == 0 {
		o.allSent = true
	}
	return outputs, o.outputCursor, nil
}

// finished returns whether the final page of output has been returned.
func (o *output) finished() bool {
	o.cursorLock.Lock()
	defer o.cursorLock.Unlock()
	return o.allSent
}

// handleInput routes data to the module. For batch directives the input is a JSON
// map of files that are written to the input directory, and the module is started
// (if requested) once they are staged. For streaming directives the module is
// started if it isn't already running, and the input is written to standard-in.
func (apis *APIService) handleInput(inputBytes []byte, startModule bool) error {
	apis.moduleLock.Lock()
	defer apis.moduleLock.Unlock()

	if !apis.streaming {
		if apis.activeModule != nil {
			return errModuleRunning
		}
		if err := handleFileInput(inputBytes, apis.inputDirectory); err != nil {
			return err
		}
		if startModule {
			return apis.startModule()
		}
		return nil
	}

	if apis.activeModule == nil {
		if err := apis.startModule(); err != nil {
			return err
		}
	}
	if apis.inputClosed {
		return errInputClosed
	}
	return apis.activeModule.Write(inputBytes)
}

func handleFileInput(inputBytes []byte, inputDirectory string) error {
	fileMapping := make(map[string][]byte)
	if err := json.Unmarshal(inputBytes, &fileMapping); err != nil {
		return err
	}
	if info, err := os.Stat(inputDirectory); err != nil || !info.IsDir() {
		if err = os.Mkdir(inputDirectory, 0777); err != nil {
			return err
		}
	}
	for filename, data := range fileMapping {
		if err := os.WriteFile(path.Join(inputDirectory, filename), data, 0666); err != nil {
			return err
		}
	}
	return nil
}

// startModule attempts to start the module via the wrapper script, communicating
// the channel listener either the new module or failure. For streaming directives
// this also starts collecting the module's standard-out. The caller must hold the
// moduleLock.
func (apis *APIService) startModule() error {
	activeModule, err := apis.startFn(apis.runtimeConfig.Environment(), apis.streaming)
	if err != nil {
		apis.logger.Error("module started with error", "error", err)
	} else {
		apis.logger.Debug("module started")
		apis.activeModule = activeModule
		if apis.streaming {
			apis.stream = newStream()
			go apis.stream.pump(activeModule, apis.logger, func(collected bool) {
				if collected {
					apis.requestShutdown()
				} else {
					apis.startCollectionTimer()
				}
			})
		}
	}
	select {
	case apis.moduleChannel <- err:
	default:
	}
	return err
}

func (apis *APIService) handleOutputAsync(outputCh chan *output) {
	out := apis.handleOutput()
	apis.startCollectionTimer()
	outputCh <- out
}

func (apis *APIService) handleOutput() *output {
	outputFilenames, err := apis.handleFullOutput()
	return &output{
		outputFilenames:   outputFilenames,
		outputCursor:      0,
		maximumOutputSize: apis.maximumOutputSize,
		err:               err,
	}
}

// handleFullOutput retrieves the names of all files the batch module wrote to the
// output directory. This routine will Wait() on the module before returning.
func (apis *APIService) handleFullOutput() ([]string, error) {
	// wait for the module to complete to ensure all files are written
	if err := apis.activeModule.WaitForStop(); err != nil {
		apis.logger.Error("waitForStop error", "error", err)
		return nil, err
	}

	// collect the names of all output files, including those one level down in run directories
	outputRunDirs, err := os.ReadDir(apis.outputDirectory)
	if err != nil {
		apis.logger.Error("readDir error", "error", err)
		return nil, err
	}
	names := make([]string, 0, len(outputRunDirs))
	for _, entry := range outputRunDirs {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		} else if runFiles, err := os.ReadDir(path.Join(apis.outputDirectory, entry.Name())); err != nil {
			return nil, err
		} else {
			for _, runEntry := range runFiles {
				names = append(names, path.Join(entry.Name(), runEntry.Name()))
			}
		}
	}
	if apis.logger.Enabled(context.Background(), slog.LevelDebug) {
		apis.logger.Debug("output files", "fileList", strings.Join(names, ","))
	}
	return names, nil
}
