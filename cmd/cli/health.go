// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NodeHealth represents the health status of a single node
type NodeHealthStatus struct {
	NodeID      string `json:"node_id"`
	Address     string `json:"address"`
	Status      string `json:"status"`
	IsLeader    bool   `json:"is_leader"`
	LastContact string `json:"last_contact,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ClusterHealthStatus represents the overall cluster health
type ClusterHealthStatus struct {
	ClusterStatus string             `json:"cluster_status"`
	LeaderID      string             `json:"leader_id"`
	LeaderAddr    string             `json:"leader_addr"`
	TotalNodes    int                `json:"total_nodes"`
	HealthyNodes  int                `json:"healthy_nodes"`
	Nodes         []NodeHealthStatus `json:"nodes"`
	Timestamp     string             `json:"timestamp"`
}

// HealthResponse represents individual node health
type HealthResponse struct {
	Status     string `json:"status"`
	NodeID     string `json:"node_id"`
	IsLeader   bool   `json:"is_leader"`
	State      string `json:"state"`
	LeaderID   string `json:"leader_id,omitempty"`
	LeaderAddr string `json:"leader_addr,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// checkNodeHealth checks the health of a single node
func checkNodeHealth(nodeAddr string) (*HealthResponse, error) {
	url := fmt.Sprintf("http://%s/health", nodeAddr)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to node %s: %v", nodeAddr, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var health HealthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	return &health, nil
}

// checkClusterHealth checks the overall cluster health
func checkClusterHealth(nodeAddr string) (*ClusterHealthStatus, error) {
	url := fmt.Sprintf("http://%s/cluster/health", nodeAddr)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to node %s: %v", nodeAddr, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var clusterHealth ClusterHealthStatus
	if err := json.Unmarshal(body, &clusterHealth); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	return &clusterHealth, nil
}

// printNodeHealth prints formatted node health information
func printNodeHealth(health *HealthResponse) {
	fmt.Printf("Node Health Status:\n")
	fmt.Printf("  Node ID: %s\n", health.NodeID)
	fmt.Printf("  Status: %s\n", health.Status)
	fmt.Printf("  State: %s\n", health.State)
	fmt.Printf("  Is Leader: %v\n", health.IsLeader)
	if health.LeaderID != "" {
		fmt.Printf("  Leader ID: %s\n", health.LeaderID)
	}
	if health.LeaderAddr != "" {
		fmt.Printf("  Leader Address: %s\n", health.LeaderAddr)
	}
	fmt.Printf("  Timestamp: %s\n", health.Timestamp)
}

// printClusterHealth prints formatted cluster health information
func printClusterHealth(clusterHealth *ClusterHealthStatus) {
	fmt.Printf("Cluster Health Status:\n")
	fmt.Printf("  Cluster Status: %s\n", clusterHealth.ClusterStatus)
	fmt.Printf("  Total Nodes: %d\n", clusterHealth.TotalNodes)
	fmt.Printf("  Healthy Nodes: %d\n", clusterHealth.HealthyNodes)
	fmt.Printf("  Leader ID: %s\n", clusterHealth.LeaderID)
	fmt.Printf("  Leader Address: %s\n", clusterHealth.LeaderAddr)
	fmt.Printf("  Timestamp: %s\n\n", clusterHealth.Timestamp)

	fmt.Printf("Node Details:\n")
	for _, node := range clusterHealth.Nodes {
		fmt.Printf("  - Node ID: %s\n", node.NodeID)
		fmt.Printf("    Address: %s\n", node.Address)
		fmt.Printf("    Status: %s\n", node.Status)
		fmt.Printf("    Is Leader: %v\n", node.IsLeader)
		if node.Error != "" {
			fmt.Printf("    Error: %s\n", node.Error)
		}
		fmt.Printf("\n")
	}
}

// runHealthCheck is the main function for health checking
func runHealthCheck(nodeAddr string, checkCluster bool) error {
	if checkCluster {
		clusterHealth, err := checkClusterHealth(nodeAddr)
		if err != nil {
			return fmt.Errorf("cluster health check failed: %v", err)
		}
		printClusterHealth(clusterHealth)

		// Set exit code based on cluster health
		switch clusterHealth.ClusterStatus {
		case "healthy":
			return nil
		case "degraded":
			fmt.Printf("Warning: Cluster is in degraded state\n")
			return fmt.Errorf("cluster degraded")
		case "critical":
			fmt.Printf("Error: Cluster is in critical state\n")
			return fmt.Errorf("cluster critical")
		}
	} else {
		health, err := checkNodeHealth(nodeAddr)
		if err != nil {
			return fmt.Errorf("node health check failed: %v", err)
		}
		printNodeHealth(health)

		// Set exit code based on node health
		if health.Status != "healthy" {
			return fmt.Errorf("node unhealthy: %s", health.Status)
		}
	}

	return nil
}
