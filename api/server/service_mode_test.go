/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/tranquildata/platform-module/config"
	"github.com/tranquildata/platform-module/module"
)

// fakeModule runs the given logic in a goroutine in place of a real process.
// Streaming modules are connected with in-memory pipes.
type fakeModule struct {
	stdinWriter  *io.PipeWriter
	stdoutReader *io.PipeReader
	result       chan error
}

func startFake(streaming bool, run func(stdin io.Reader, stdout io.Writer) error) *fakeModule {
	fm := &fakeModule{result: make(chan error, 1)}
	var stdin io.Reader
	var stdout io.Writer = io.Discard
	var stdoutWriter *io.PipeWriter
	if streaming {
		stdin, fm.stdinWriter = io.Pipe()
		fm.stdoutReader, stdoutWriter = io.Pipe()
		stdout = stdoutWriter
	}
	go func() {
		err := run(stdin, stdout)
		if stdoutWriter != nil {
			stdoutWriter.Close()
		}
		fm.result <- err
	}()
	return fm
}

func (fm *fakeModule) WaitForStop() error {
	return <-fm.result
}

func (fm *fakeModule) Read(buffer []byte) (int, error) {
	return fm.stdoutReader.Read(buffer)
}

func (fm *fakeModule) Write(message []byte) error {
	_, err := fm.stdinWriter.Write(message)
	return err
}

func (fm *fakeModule) CloseInput() error {
	return fm.stdinWriter.Close()
}

func testService(t *testing.T, streaming bool, run func(stdin io.Reader, stdout io.Writer) error) (*APIService, *http.ServeMux) {
	apis := Setup(&config.RuntimeConfig{Directive: "test"}, streaming)
	apis.waitTimeout = 100 * time.Millisecond
	apis.inputDirectory = t.TempDir()
	apis.outputDirectory = t.TempDir()
	apis.startFn = func(_ map[string]string, s bool) (module.Module, error) {
		if s != streaming {
			t.Errorf("module started with streaming=%t, expected %t", s, streaming)
		}
		return startFake(streaming, run), nil
	}
	mux := http.NewServeMux()
	apis.registerHTTPEndpoints(mux)
	return apis, mux
}

func call(t *testing.T, mux *http.ServeMux, method string, target string, body string) (int, *OutputResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, target, strings.NewReader(body)))
	if w.Code != http.StatusOK {
		return w.Code, nil
	}
	resp := &OutputResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), resp); err != nil {
		t.Fatalf("%s %s: bad response %q: %s", method, target, w.Body.String(), err.Error())
	}
	return w.Code, resp
}

func callStatus(mux *http.ServeMux, method string, target string, body string) int {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, target, strings.NewReader(body)))
	return w.Code
}

func expectShutdown(t *testing.T, apis *APIService, expected bool) {
	t.Helper()
	select {
	case <-apis.shutdownChannel:
		if !expected {
			t.Error("unexpected shutdown")
		}
	case <-time.After(500 * time.Millisecond):
		if expected {
			t.Error("expected shutdown")
		}
	}
}

func Test_thatOptionsReportsMode(t *testing.T) {
	for _, streaming := range []bool{true, false} {
		_, mux := testService(t, streaming, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/", nil))
		info := &ServiceInfo{}
		if err := json.Unmarshal(w.Body.Bytes(), info); err != nil {
			t.Fatal(err)
		} else if info.Directive != "test" || info.Streaming != streaming {
			t.Errorf("unexpected service info %+v", info)
		}
	}
}

func Test_thatStreamingRespondsAndShutsDown(t *testing.T) {
	//the equivalent of the bundled "hello" module
	apis, mux := testService(t, true, func(stdin io.Reader, stdout io.Writer) error {
		var name string
		fmt.Fscan(stdin, &name)
		fmt.Fprintf(stdout, "hello %s\n", name)
		return nil
	})
	code, resp := call(t, mux, http.MethodPut, "/", "alice")
	if code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	} else if out := string(resp.Outputs[""]); out != "hello alice\n" {
		t.Errorf("unexpected output %q", out)
	}
	expectShutdown(t, apis, true)
}

func Test_thatStreamingSustainsInteraction(t *testing.T) {
	apis, mux := testService(t, true, func(stdin io.Reader, stdout io.Writer) error {
		scanner := bufio.NewScanner(stdin)
		for scanner.Scan() {
			fmt.Fprintf(stdout, "echo %s\n", scanner.Text())
		}
		return nil
	})
	for _, input := range []string{"one", "two", "three"} {
		if code, resp := call(t, mux, http.MethodPut, "/", input); code != http.StatusOK {
			t.Fatalf("unexpected status %d", code)
		} else if out := string(resp.Outputs[""]); out != "echo "+input+"\n" {
			t.Errorf("unexpected output %q", out)
		}
	}
	//no pending output, so this waits out the timeout and returns nothing
	if code, resp := call(t, mux, http.MethodGet, "/", ""); code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	} else if out := resp.Outputs[""]; len(out) != 0 {
		t.Errorf("unexpected output %q", out)
	}
	expectShutdown(t, apis, false)
}

func Test_thatStreamingOutputDoesNotWaitForInput(t *testing.T) {
	release := make(chan struct{})
	apis, mux := testService(t, true, func(stdin io.Reader, stdout io.Writer) error {
		bufio.NewReader(stdin).ReadString('\n') //nolint:errcheck
		fmt.Fprintln(stdout, "started")
		<-release
		fmt.Fprintln(stdout, "finished")
		return nil
	})
	if code, resp := call(t, mux, http.MethodPut, "/", "go"); code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	} else if out := string(resp.Outputs[""]); out != "started\n" {
		t.Errorf("unexpected output %q", out)
	}
	expectShutdown(t, apis, false)

	close(release)
	if code, resp := call(t, mux, http.MethodGet, "/", ""); code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	} else if out := string(resp.Outputs[""]); out != "finished\n" {
		t.Errorf("unexpected output %q", out)
	}
	expectShutdown(t, apis, true)
}

func Test_thatClosingStreamingInputSignalsEOF(t *testing.T) {
	//a module that reads all input before producing any output
	apis, mux := testService(t, true, func(stdin io.Reader, stdout io.Writer) error {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "read %d lines\n", strings.Count(string(data), "\n"))
		return nil
	})
	for _, input := range []string{"one", "two"} {
		if code, resp := call(t, mux, http.MethodPut, "/", input); code != http.StatusOK {
			t.Fatalf("unexpected status %d", code)
		} else if out := resp.Outputs[""]; len(out) != 0 {
			t.Errorf("unexpected output %q", out)
		}
	}
	if code, resp := call(t, mux, http.MethodDelete, "/", ""); code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	} else if out := string(resp.Outputs[""]); out != "read 2 lines\n" {
		t.Errorf("unexpected output %q", out)
	}
	expectShutdown(t, apis, true)
	if code := callStatus(mux, http.MethodPut, "/", "three"); code != http.StatusConflict {
		t.Errorf("input after close: unexpected status %d", code)
	}
}

func Test_thatClosingInputRequiresARunningStreamingModule(t *testing.T) {
	_, mux := testService(t, true, nil)
	if code := callStatus(mux, http.MethodDelete, "/", ""); code != http.StatusBadRequest {
		t.Errorf("streaming before start: unexpected status %d", code)
	}
	_, mux = testService(t, false, nil)
	if code := callStatus(mux, http.MethodDelete, "/", ""); code != http.StatusBadRequest {
		t.Errorf("batch: unexpected status %d", code)
	}
}

// startUncollectedStream sends input to a streaming module that, once released,
// writes output and terminates without that output being collected.
func startUncollectedStream(t *testing.T, apis *APIService, mux *http.ServeMux) {
	t.Helper()
	release := make(chan struct{})
	apis.startFn = func(_ map[string]string, _ bool) (module.Module, error) {
		return startFake(true, func(stdin io.Reader, stdout io.Writer) error {
			bufio.NewReader(stdin).ReadString('\n') //nolint:errcheck
			<-release
			fmt.Fprintln(stdout, "late output")
			return nil
		}), nil
	}
	apis.waitTimeout = 0 //return without waiting for output
	if code := callStatus(mux, http.MethodPut, "/", "go"); code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	}
	close(release)
	select {
	case <-apis.stream.done:
	case <-time.After(time.Second):
		t.Fatal("module did not terminate")
	}
}

func Test_thatStreamingWaitsForOutputCollection(t *testing.T) {
	apis, mux := testService(t, true, nil)
	startUncollectedStream(t, apis, mux)
	//the module has terminated, but its output hasn't been collected
	expectShutdown(t, apis, false)
	if code, resp := call(t, mux, http.MethodGet, "/", ""); code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	} else if out := string(resp.Outputs[""]); out != "late output\n" {
		t.Errorf("unexpected output %q", out)
	}
	expectShutdown(t, apis, true)
}

func Test_thatUncollectedStreamingOutputTimesOut(t *testing.T) {
	apis, mux := testService(t, true, nil)
	apis.collectionTimeout = 200 * time.Millisecond
	startUncollectedStream(t, apis, mux)
	expectShutdown(t, apis, true)
}

func Test_thatStreamingReportsModuleFailure(t *testing.T) {
	_, mux := testService(t, true, func(stdin io.Reader, stdout io.Writer) error {
		bufio.NewReader(stdin).ReadString('\n') //nolint:errcheck
		return fmt.Errorf("exit status 1")
	})
	if code, _ := call(t, mux, http.MethodPut, "/", "input"); code != http.StatusInternalServerError {
		t.Errorf("unexpected status %d", code)
	}
}

func Test_thatStreamingRejectsAsyncAPI(t *testing.T) {
	_, mux := testService(t, true, nil)
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		if code := callStatus(mux, method, "/async?runId=1", "{}"); code != http.StatusBadRequest {
			t.Errorf("%s /async: unexpected status %d", method, code)
		}
	}
}

func Test_thatBatchRejectsStreamingOutput(t *testing.T) {
	_, mux := testService(t, false, nil)
	if code := callStatus(mux, http.MethodGet, "/", ""); code != http.StatusBadRequest {
		t.Errorf("unexpected status %d", code)
	}
}

func Test_thatBatchStagesFilesAndReturnsOutput(t *testing.T) {
	var apis *APIService
	apis, mux := testService(t, false, func(_ io.Reader, _ io.Writer) error {
		//all input must be staged before the module starts
		entries, err := os.ReadDir(apis.inputDirectory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			data, err := os.ReadFile(path.Join(apis.inputDirectory, entry.Name()))
			if err != nil {
				return err
			}
			if err = os.WriteFile(path.Join(apis.outputDirectory, entry.Name()+".out"), append(data, '!'), 0666); err != nil {
				return err
			}
		}
		return nil
	})
	input, err := json.Marshal(map[string][]byte{
		"a": []byte("first"),
		"b": []byte("second"),
	})
	if err != nil {
		t.Fatal(err)
	}
	code, resp := call(t, mux, http.MethodPut, "/", string(input))
	if code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	}
	expected := map[string]string{"a.out": "first!", "b.out": "second!"}
	if len(resp.Outputs) != len(expected) {
		t.Errorf("unexpected outputs %v", resp.Outputs)
	}
	for name, data := range expected {
		if string(resp.Outputs[name]) != data {
			t.Errorf("%s: expected %q, got %q", name, data, resp.Outputs[name])
		}
	}
	expectShutdown(t, apis, true)
}

func Test_thatBatchReturnsAllOutputWithoutPaging(t *testing.T) {
	var apis *APIService
	apis, mux := testService(t, false, func(_ io.Reader, _ io.Writer) error {
		for _, name := range []string{"a", "b", "c"} {
			if err := os.WriteFile(path.Join(apis.outputDirectory, name), []byte("output "+name), 0666); err != nil {
				return err
			}
		}
		return nil
	})
	apis.maximumOutputSize = 1 //would end a page after every file
	code, resp := call(t, mux, http.MethodPut, "/", "{}")
	if code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	} else if len(resp.Outputs) != 3 {
		t.Errorf("expected 3 outputs, got %v", resp.Outputs)
	} else if resp.CursorOffset != 0 {
		t.Errorf("unexpected cursor offset %d", resp.CursorOffset)
	}
}

func Test_thatAsyncBatchShutsDownAfterFinalPage(t *testing.T) {
	release := make(chan struct{})
	var apis *APIService
	apis, mux := testService(t, false, func(_ io.Reader, _ io.Writer) error {
		<-release
		for _, name := range []string{"a", "b", "c"} {
			if err := os.WriteFile(path.Join(apis.outputDirectory, name), []byte("output "+name), 0666); err != nil {
				return err
			}
		}
		return nil
	})
	apis.maximumOutputSize = 1 //one file per page
	if code := callStatus(mux, http.MethodPut, "/async?runId=1", "{}"); code != http.StatusOK {
		t.Fatalf("start: unexpected status %d", code)
	}
	//still running, so the caller is told to retry
	if code := callStatus(mux, http.MethodGet, "/async?runId=1", ""); code != http.StatusGatewayTimeout {
		t.Errorf("while running: unexpected status %d", code)
	}
	expectShutdown(t, apis, false)

	close(release)
	collected := map[string]bool{}
	for page := 1; page <= 3; page++ {
		code, resp := call(t, mux, http.MethodGet, "/async?runId=1", "")
		if code != http.StatusOK {
			t.Fatalf("page %d: unexpected status %d", page, code)
		}
		for name := range resp.Outputs {
			collected[name] = true
		}
		if page < 3 {
			if resp.CursorOffset == 0 {
				t.Fatalf("page %d: output ended early", page)
			}
			expectShutdown(t, apis, false)
		} else if resp.CursorOffset != 0 {
			t.Errorf("final page: unexpected cursor offset %d", resp.CursorOffset)
		}
	}
	if len(collected) != 3 {
		t.Errorf("expected 3 outputs, got %v", collected)
	}
	expectShutdown(t, apis, true)
}

func Test_thatAsyncBatchShutsDownWithoutOutput(t *testing.T) {
	apis, mux := testService(t, false, func(_ io.Reader, _ io.Writer) error {
		return nil
	})
	if code := callStatus(mux, http.MethodPut, "/async?runId=1", "{}"); code != http.StatusOK {
		t.Fatalf("start: unexpected status %d", code)
	}
	if code := callStatus(mux, http.MethodGet, "/async?runId=1", ""); code != http.StatusNoContent {
		t.Errorf("unexpected status %d", code)
	}
	expectShutdown(t, apis, true)
}

func Test_thatAsyncBatchShutsDownAfterReportingFailure(t *testing.T) {
	apis, mux := testService(t, false, func(_ io.Reader, _ io.Writer) error {
		return fmt.Errorf("exit status 1")
	})
	if code := callStatus(mux, http.MethodPut, "/async?runId=1", "{}"); code != http.StatusOK {
		t.Fatalf("start: unexpected status %d", code)
	}
	if code := callStatus(mux, http.MethodGet, "/async?runId=1", ""); code != http.StatusInternalServerError {
		t.Errorf("unexpected status %d", code)
	}
	expectShutdown(t, apis, true)
}

func Test_thatUncollectedAsyncOutputTimesOut(t *testing.T) {
	apis, mux := testService(t, false, func(_ io.Reader, _ io.Writer) error {
		return nil
	})
	apis.collectionTimeout = 200 * time.Millisecond
	if code := callStatus(mux, http.MethodPut, "/async?runId=1", "{}"); code != http.StatusOK {
		t.Fatalf("start: unexpected status %d", code)
	}
	expectShutdown(t, apis, true)
}

func Test_thatBatchRejectsInputAfterStart(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	_, mux := testService(t, false, func(_ io.Reader, _ io.Writer) error {
		<-release
		return nil
	})
	if code := callStatus(mux, http.MethodPut, "/async?runId=1&uploadOnly=true", `{"a": "Zmlyc3Q="}`); code != http.StatusOK {
		t.Fatalf("upload: unexpected status %d", code)
	}
	if code := callStatus(mux, http.MethodPut, "/async?runId=1", `{"b": "c2Vjb25k"}`); code != http.StatusOK {
		t.Fatalf("start: unexpected status %d", code)
	}
	if code := callStatus(mux, http.MethodPut, "/async?runId=1&uploadOnly=true", `{"c": "dGhpcmQ="}`); code != http.StatusConflict {
		t.Errorf("late upload: unexpected status %d", code)
	}
}
