/*
 * Copyright (c) 2026, Tranquil Data, Inc. All rights reserved.
 */

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func Test_thatEndpointsRegister(t *testing.T) {
	//http.ServeMux panics on conflicting patterns, e.g. HEAD / and GET /async
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registering endpoints panicked: %v", r)
		}
	}()
	(&APIService{}).registerHTTPEndpoints(http.NewServeMux())
}

func Test_thatHeadChecksStatus(t *testing.T) {
	mux := http.NewServeMux()
	(&APIService{}).registerHTTPEndpoints(mux)
	cases := []struct {
		target string
		status int
	}{
		{"/", http.StatusOK},
		{"/?podId=1", http.StatusOK},
		{"/unknown", http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodHead, c.target, nil))
		if w.Code != c.status {
			t.Errorf("HEAD %s: expected %d, got %d", c.target, c.status, w.Code)
		} else if c.status == http.StatusOK && w.Body.Len() != 0 {
			t.Errorf("HEAD %s: expected no body, got %q", c.target, w.Body.String())
		}
	}
}
