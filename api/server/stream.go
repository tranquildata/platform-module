/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package server

import (
	"bytes"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/tranquildata/platform-module/module"
)

// stream continuously collects standard-out from a streaming module so that the
// module never blocks on output, independent of when (or whether) input arrives.
type stream struct {
	lock   sync.Mutex
	buffer bytes.Buffer
	ready  chan struct{} //holds a token while there is uncollected output
	done   chan struct{} //closed once the module has terminated
	err    error         //set (if any) before done is closed
}

func newStream() *stream {
	return &stream{
		ready: make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
}

// pump reads the module's standard-out until EOF, then waits for the module to
// terminate. If all output has already been collected by then, onFinished is called.
func (s *stream) pump(m module.Module, logger *slog.Logger, onFinished func()) {
	chunk := make([]byte, 32*1024)
	var readErr error
	for {
		n, err := m.Read(chunk)
		if n > 0 {
			s.lock.Lock()
			s.buffer.Write(chunk[:n])
			select {
			case s.ready <- struct{}{}:
			default:
			}
			s.lock.Unlock()
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}

	err := m.WaitForStop()
	if err == nil {
		err = readErr
	}
	if err != nil {
		logger.Error("streaming module terminated with error", "error", err)
	} else {
		logger.Debug("streaming module terminated")
	}

	s.lock.Lock()
	s.err = err
	close(s.done)
	empty := s.buffer.Len() == 0
	s.lock.Unlock()

	if empty {
		onFinished()
	}
}

// collect returns all output produced since the last collection. If no output is
// pending, it first waits up to timeout for some to arrive or for the module to
// terminate. The returned flag is true once the module has terminated and all of
// its output has been collected.
func (s *stream) collect(timeout time.Duration) (*output, bool) {
	s.lock.Lock()
	pending := s.buffer.Len() > 0
	s.lock.Unlock()

	if !pending {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-s.ready:
		case <-s.done:
		case <-timer.C:
		}
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	select {
	case <-s.ready:
	default:
	}
	outputBytes := bytes.Clone(s.buffer.Bytes())
	s.buffer.Reset()

	finished := false
	out := &output{
		outputBytes: outputBytes,
	}
	select {
	case <-s.done:
		finished = true
		out.err = s.err
	default:
	}
	return out, finished
}
