// Copyright 2023 The Casbin Mesh Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type Config struct {
	ErrorHandler func(ctx context.Context, err error, w http.ResponseWriter)
}

func DefaultConfig() Config {
	return Config{ErrorHandler: DefaultErrorHandler}
}

type errorWrapper struct {
	Error string `json:"error"`
}

func err2code(err error) int {
	if err == nil {
		return http.StatusOK
	}

	errMsg := err.Error()

	// Authentication/Authorization errors
	if strings.Contains(errMsg, "unauthorized") || strings.Contains(errMsg, "not authorized") {
		return http.StatusUnauthorized
	}
	if strings.Contains(errMsg, "forbidden") || strings.Contains(errMsg, "permission denied") {
		return http.StatusForbidden
	}

	// Client errors - Bad Request (match actual error strings from go-playground/validator)
	if strings.Contains(errMsg, "validation failed") || strings.Contains(errMsg, "unmarshal failed") ||
		strings.Contains(errMsg, "invalid") || strings.Contains(errMsg, "bad request") ||
		strings.Contains(errMsg, "required") || strings.Contains(errMsg, "Field validation") ||
		strings.Contains(errMsg, "Key:") { // go-playground/validator error format
		return http.StatusBadRequest
	}

	// Request entity too large (check before other errors)
	if strings.Contains(errMsg, "batch size too large") || strings.Contains(errMsg, "too large") ||
		strings.Contains(errMsg, "limit exceeded") || strings.Contains(errMsg, "http: request body too large") {
		return http.StatusRequestEntityTooLarge
	}

	// Resource not found (match exact error strings from the project)
	if strings.Contains(errMsg, "namespace not exist") || strings.Contains(errMsg, "not found") ||
		strings.Contains(errMsg, "does not exist") {
		return http.StatusNotFound
	}

	// Precondition failed - resource state issues (match exact error strings)
	if strings.Contains(errMsg, "model unset yet") || strings.Contains(errMsg, "namespace already existed") {
		return http.StatusPreconditionFailed
	}

	// Service unavailable - raft/cluster issues (match exact error strings)
	if strings.Contains(errMsg, "not leader") || strings.Contains(errMsg, "no leader") ||
		strings.Contains(errMsg, "cluster") || strings.Contains(errMsg, "raft") {
		return http.StatusServiceUnavailable
	}

	// Request timeout - raft/storage timeouts (match exact error strings)
	if strings.Contains(errMsg, "timeout waiting for initial logs application") ||
		strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline exceeded") {
		return http.StatusRequestTimeout
	}

	// Conflict - concurrent modification
	if strings.Contains(errMsg, "conflict") || strings.Contains(errMsg, "concurrent") {
		return http.StatusConflict
	}

	// Stale read (custom status for consistency)
	if strings.Contains(errMsg, "stale read") {
		return http.StatusNotAcceptable // 406
	}

	// Default to Internal Server Error for unknown errors
	return http.StatusInternalServerError
}

func ErrorEncoder(_ context.Context, err error, w http.ResponseWriter) {
	statusCode := err2code(err)
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(errorWrapper{Error: err.Error()})
}

var (
	DefaultErrorHandler = ErrorEncoder
)
