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

package store

import (
	"context"
	"testing"
	"time"
)

func TestAutomaticSnapshotTruncatesRaftLogs(t *testing.T) {
	s := mustNewStoreAtPath(t.TempDir())
	defer s.ln.Close()
	s.SnapshotThreshold = 8
	s.SnapshotInterval = 20 * time.Millisecond
	if err := s.Open(true); err != nil {
		t.Fatal(err)
	}
	defer s.Close(true)
	if _, err := s.WaitForLeader(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.CreateNamespace(ctx, "default"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModelFromString(ctx, "default", modelText); err != nil {
		t.Fatal(err)
	}
	// These read commands are also logged. Keep the model immutable while
	// automatic snapshots run and enough entries accumulate for truncation.
	for i := 0; i < 32; i++ {
		if _, err := s.ListNamespace(ctx); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		first, last, err := s.boltStore.Indexes()
		if err != nil {
			t.Fatal(err)
		}
		if first > 1 {
			if retained := last - first + 1; retained < s.numTrailingLogs {
				t.Fatalf("truncation lost trailing logs: retained=%d want >=%d", retained, s.numTrailingLogs)
			}
			if s.raft.Stats()["last_snapshot_index"] == "0" {
				t.Fatal("logs truncated without a completed snapshot")
			}
			t.Logf("retained log range: %d..%d, last snapshot: %s", first, last, s.raft.Stats()["last_snapshot_index"])
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("automatic snapshot did not truncate old logs")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
