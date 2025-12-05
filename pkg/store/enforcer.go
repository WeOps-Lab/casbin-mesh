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
	"encoding/json"
	"fmt"
	"log"
	"time"

	_const "github.com/casbin/casbin-mesh/pkg/const"
	"github.com/casbin/casbin-mesh/proto/command"
	"github.com/casbin/casbin/v2"
	"github.com/golang/protobuf/proto"
	"github.com/hashicorp/raft"
)

const (
	SystemEnforce = ".system"
)

// CreateNamespace creates a new namespace.
func (s *Store) CreateNamespace(ctx context.Context, ns string) error {
	cmd, err := proto.Marshal(&command.Command{
		Type:      command.Type_COMMAND_TYPE_CREATE_NAMESPACE,
		Namespace: ns,
		Payload:   nil,
		Metadata:  nil,
	})
	if err != nil {
		return err
	}
	f := s.raft.Apply(cmd, s.ApplyTimeout)
	if e := f.(raft.Future); e.Error() != nil {
		if e.Error() == raft.ErrNotLeader {
			return ErrNotLeader
		}
		return e.Error()
	}
	r := f.Response().(*FSMResponse)
	return r.error
}

// SetModelFromString sets casbin model from string.
func (s *Store) SetModelFromString(ctx context.Context, ns string, text string) error {
	payload, err := proto.Marshal(&command.SetModelFromString{
		Text: text,
	})
	if err != nil {
		return err
	}

	cmd, err := proto.Marshal(&command.Command{
		Type:      command.Type_COMMAND_TYPE_SET_MODEL,
		Namespace: ns,
		Payload:   payload,
		Metadata:  nil,
	})
	if err != nil {
		return err
	}
	f := s.raft.Apply(cmd, s.ApplyTimeout)
	if e := f.(raft.Future); e.Error() != nil {
		if e.Error() == raft.ErrNotLeader {
			return ErrNotLeader
		}
		return e.Error()
	}
	r := f.Response().(*FSMResponse)
	return r.error
}

// Enforce executes enforcement.
func (s *Store) Enforce(ctx context.Context, ns string, level command.EnforcePayload_Level, freshness int64, params ...interface{}) (bool, error) {
	log.Printf("[ENFORCE] Starting enforcement: namespace=%s, level=%v, freshness=%d, params=%v", ns, level, freshness, params)

	if level == command.EnforcePayload_QUERY_REQUEST_LEVEL_STRONG {
		log.Printf("[ENFORCE] Processing STRONG level enforcement for namespace=%s", ns)

		var B [][]byte
		for i, p := range params {
			b, err := json.Marshal(p)
			if err != nil {
				log.Printf("[ENFORCE] ERROR: Failed to marshal param[%d]=%v for namespace=%s: %v", i, p, ns, err)
				return false, fmt.Errorf("failed to marshal param[%d]: %w", i, err)
			}
			B = append(B, b)
		}
		log.Printf("[ENFORCE] Successfully marshaled %d parameters for namespace=%s", len(B), ns)

		payload, err := proto.Marshal(&command.EnforcePayload{
			B:         B,
			Level:     level,
			Freshness: freshness,
		})
		if err != nil {
			log.Printf("[ENFORCE] ERROR: Failed to marshal EnforcePayload for namespace=%s: %v", ns, err)
			return false, fmt.Errorf("failed to marshal EnforcePayload: %w", err)
		}

		cmd, err := proto.Marshal(&command.Command{
			Type:      command.Type_COMMAND_TYPE_ENFORCE_REQUEST,
			Namespace: ns,
			Payload:   payload,
			Metadata:  nil,
		})
		if err != nil {
			log.Printf("[ENFORCE] ERROR: Failed to marshal Command for namespace=%s: %v", ns, err)
			return false, fmt.Errorf("failed to marshal Command: %w", err)
		}

		log.Printf("[ENFORCE] Applying raft command for namespace=%s, timeout=%v", ns, s.ApplyTimeout)
		f := s.raft.Apply(cmd, s.ApplyTimeout)
		if e := f.(raft.Future); e.Error() != nil {
			if e.Error() == raft.ErrNotLeader {
				log.Printf("[ENFORCE] ERROR: Not leader when applying raft command for namespace=%s", ns)
				return false, ErrNotLeader
			}
			log.Printf("[ENFORCE] ERROR: Raft apply failed for namespace=%s: %v", ns, e.Error())
			return false, fmt.Errorf("raft apply failed: %w", e.Error())
		}

		r := f.Response().(*FSMEnforceResponse)
		if r.error != nil {
			log.Printf("[ENFORCE] ERROR: FSM response error for namespace=%s: %v", ns, r.error)
		} else {
			log.Printf("[ENFORCE] SUCCESS: STRONG level enforcement completed for namespace=%s, result=%t", ns, r.ok)
		}
		return r.ok, r.error
	}

	if level == command.EnforcePayload_QUERY_REQUEST_LEVEL_WEAK && s.raft.State() != raft.Leader {
		log.Printf("[ENFORCE] ERROR: WEAK level requires leader but current state=%v for namespace=%s", s.raft.State(), ns)
		return false, ErrNotLeader
	}

	if level == command.EnforcePayload_QUERY_REQUEST_LEVEL_NONE &&
		freshness > 0 &&
		s.raft.State() != raft.Leader &&
		time.Since(s.raft.LastContact()).Nanoseconds() > freshness {
		lastContact := time.Since(s.raft.LastContact()).Nanoseconds()
		log.Printf("[ENFORCE] ERROR: Stale read detected for namespace=%s, freshness=%d, lastContact=%d", ns, freshness, lastContact)
		return false, ErrStaleRead
	}

	log.Printf("[ENFORCE] Loading enforcer for namespace=%s", ns)
	if e, ok := s.enforcers.Load(ns); ok {
		enforcer := e.(*casbin.DistributedEnforcer)
		if enforcer == nil {
			log.Printf("[ENFORCE] ERROR: Enforcer is nil for namespace=%s", ns)
			return false, fmt.Errorf("enforcer is nil for namespace: %s", ns)
		}

		log.Printf("[ENFORCE] Executing casbin enforcement for namespace=%s with params=%v", ns, params)
		r, err := enforcer.Enforce(params...)
		if err != nil {
			log.Printf("[ENFORCE] ERROR: Casbin enforcement failed for namespace=%s with params=%v: %v", ns, params, err)
			return false, fmt.Errorf("casbin enforcement failed: %w", err)
		}

		log.Printf("[ENFORCE] SUCCESS: Enforcement completed for namespace=%s, result=%t", ns, r)
		return r, nil
	} else {
		log.Printf("[ENFORCE] ERROR: Namespace not found: %s. Available enforcers: ", ns)
		// 记录可用的命名空间以便调试
		s.enforcers.Range(func(key, value interface{}) bool {
			log.Printf("[ENFORCE] Available namespace: %v", key)
			return true
		})
		return false, fmt.Errorf("namespace not exist: %s", ns)
	}
}

func (s *Store) InitAuth(ctx context.Context, rootUsername string) error {
	if !s.IsLeader() {
		return nil
	}
	// createNamespace
	if err := s.CreateNamespace(ctx, SystemEnforce); err != nil {
		return err
	}
	// setModelFromString
	if err := s.SetModelFromString(ctx, SystemEnforce, _const.RBACModel); err != nil {
		return err
	}
	// basic rules
	if _, err := s.AddPolicies(ctx, SystemEnforce, "g", "g", _const.SystemRules); err != nil {
		return err
	}
	return nil
}

// SetMetadata adds the metadata md to any existing metadata for
// this node.
func (s *Store) SetMetadata(md map[string]string) error {
	return s.setMetadata(s.raftID, md)
}

// setMetadata adds the metadata md to any existing metadata for
// the given node ID.
func (s *Store) setMetadata(id string, md map[string]string) error {
	// Check local data first.
	if func() bool {
		s.metaMu.RLock()
		defer s.metaMu.RUnlock()
		if _, ok := s.meta[id]; ok {
			for k, v := range md {
				if s.meta[id][k] != v {
					return false
				}
			}
			return true
		}
		return false
	}() {
		// Local data is same as data being pushed in,
		// nothing to do.
		return nil
	}

	ms := &command.MetadataSet{
		RaftId: id,
		Data:   md,
	}
	bms, err := proto.Marshal(ms)
	if err != nil {
		return err
	}

	c := &command.Command{
		Type:    command.Type_COMMAND_TYPE_METADATA_SET,
		Payload: bms,
	}
	bc, err := proto.Marshal(c)
	if err != nil {
		return err
	}

	f := s.raft.Apply(bc, s.ApplyTimeout)
	if e := f.(raft.Future); e.Error() != nil {
		if e.Error() == raft.ErrNotLeader {
			return ErrNotLeader
		}
		return e.Error()
	}

	return nil
}
