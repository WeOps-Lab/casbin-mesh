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

package main

import (
	"encoding/json"
	"flag"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestSnapshotThresholdFromFlags(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	originalArgs, originalFlags := os.Args, flag.CommandLine
	defer func() { os.Args, flag.CommandLine = originalArgs, originalFlags }()
	flag.CommandLine = flag.NewFlagSet("casmesh", flag.ContinueOnError)
	os.Args = []string{"casmesh", "--raft-address", addr, "--raft-snap", "32", "--raft-snap-int", "1h", "--pprof=false", t.TempDir()}
	cfg := parseFlags()
	closeStore := New(&cfg)
	defer closeStore()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats returned %s", resp.Status)
	}
	var stats struct {
		SnapshotThreshold uint64 `json:"snapshot_threshold"`
		TrailingLogs      uint64 `json:"trailing_logs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.SnapshotThreshold != 32 || stats.TrailingLogs != 40 {
		t.Fatalf("snapshot flags did not reach store: %+v", stats)
	}
}
