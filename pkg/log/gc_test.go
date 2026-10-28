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

package log

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	raftbadgerdb "github.com/BBVA/raft-badger"
	"github.com/dgraph-io/badger/v3"
	"github.com/hashicorp/raft"
)

func TestLogGCReclaimsTruncatedLogsWithoutGrowth(t *testing.T) {
	path := t.TempDir()
	opts := badger.DefaultOptions(path).
		WithLogger(nil).
		WithValueLogFileSize(1 << 20).
		WithValueThreshold(32).
		WithMemTableSize(1 << 20).
		WithCompactL0OnClose(true)

	// Model an existing database written without value log GC, followed by
	// Raft snapshot compaction. Keep the final entries and stable state.
	seed, err := raftbadgerdb.New(raftbadgerdb.Options{Path: path, BadgerOptions: &opts})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if seed != nil {
			seed.Close()
		}
	})
	payload := bytes.Repeat([]byte("policy"), 8192)
	for i := uint64(1); i <= 100; i++ {
		if err := seed.StoreLog(&raft.Log{Index: i, Term: 7, Type: raft.LogCommand, Data: payload}); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.SetUint64([]byte("CurrentTerm"), 7); err != nil {
		t.Fatal(err)
	}
	// Flush the live keys before deletion so LSM compaction can account for
	// the discarded values, just as it does for an existing Raft database.
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	seed, err = raftbadgerdb.New(raftbadgerdb.Options{Path: path, BadgerOptions: &opts})
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.DeleteRange(1, 90); err != nil {
		t.Fatal(err)
	}
	// Advance the transaction watermark beyond the deletion before the
	// close-time compaction; recent versions must otherwise be retained.
	for i := 0; i < 10; i++ {
		if err := seed.SetUint64([]byte("CurrentTerm"), 7); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	// Force LSM compaction to make the discard statistics deterministic.
	// This prepares the fixture; it deliberately does not run value log GC.
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Flatten(1); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	l, err := newLog(raftbadgerdb.Options{
		Path:                path,
		BadgerOptions:       &opts,
		GCThreshold:         1 << 60,
		GCInterval:          time.Hour,
		MandatoryGCInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	before := vlogBytes(t, path)
	if before < 2<<20 {
		t.Fatalf("fixture has insufficient value log data: %d bytes", before)
	}
	deadline := time.Now().Add(10 * time.Second)
	for vlogBytes(t, path) >= before {
		if time.Now().After(deadline) {
			t.Fatalf("periodic GC did not reclaim truncated logs: before=%d after=%d", before, vlogBytes(t, path))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("value log bytes: %d -> %d", before, vlogBytes(t, path))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify that GC preserves the log tail and stable state across restart.
	reopened, err := NewLogWithGCThreshold(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	first, last, err := reopened.Indexes()
	if err != nil || first != 91 || last != 100 {
		t.Fatalf("wrong retained log range: first=%d last=%d err=%v", first, last, err)
	}
	for i := uint64(91); i <= 100; i++ {
		var entry raft.Log
		if err := reopened.GetLog(i, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Term != 7 || !bytes.Equal(entry.Data, payload) {
			t.Fatalf("retained log %d was changed", i)
		}
	}
	if term, err := reopened.GetUint64([]byte("CurrentTerm")); err != nil || term != 7 {
		t.Fatalf("stable state changed: term=%d err=%v", term, err)
	}
	var removed raft.Log
	if err := reopened.GetLog(1, &removed); err != raft.ErrLogNotFound {
		t.Fatalf("truncated log is still present: %v", err)
	}
}

func vlogBytes(t *testing.T, path string) int64 {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(path, "*.vlog"))
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	for _, file := range files {
		info, err := os.Stat(file)
		if os.IsNotExist(err) {
			continue // GC may remove a file between Glob and Stat.
		}
		if err != nil {
			t.Fatal(err)
		}
		size += info.Size()
	}
	return size
}
