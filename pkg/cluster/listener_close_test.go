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

package cluster

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countedListener struct {
	net.Listener
	closes     int32
	acceptDone chan struct{}
}

func (l *countedListener) Close() error {
	atomic.AddInt32(&l.closes, 1)
	return l.Listener.Close()
}

func (l *countedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		select {
		case l.acceptDone <- struct{}{}:
		default:
		}
	}
	return conn, err
}

func TestListenerConcurrentClose(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counted := &countedListener{Listener: base, acceptDone: make(chan struct{}, 1)}
	l, err := NewListener([]net.Listener{counted}, "")
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	defer l.Close()
	address := l.Addr().String()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Close(); err != nil {
				t.Error(err)
			}
			if got := l.Addr().String(); got != address {
				t.Errorf("address changed: %s", got)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&counted.closes); got != 1 {
		t.Fatalf("underlying listener closed %d times", got)
	}
	if _, err := l.Accept(); err == nil {
		t.Fatal("Accept succeeded after Close")
	}
	select {
	case <-counted.acceptDone:
	case <-time.After(time.Second):
		t.Fatal("underlying Accept did not unblock")
	}
}
