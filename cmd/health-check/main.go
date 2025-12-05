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
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

var (
	nodeAddr     = flag.String("node", "127.0.0.1:4002", "Node address to check (host:port)")
	checkCluster = flag.Bool("cluster", false, "Check cluster health instead of single node")
	outputFormat = flag.String("format", "text", "Output format: text, json")
	timeout      = flag.Duration("timeout", 10*time.Second, "Request timeout")
	continuous   = flag.Bool("watch", false, "Continuously monitor health (every 30s)")
	interval     = flag.Duration("interval", 30*time.Second, "Monitoring interval when using -watch")
	verbose      = flag.Bool("verbose", false, "Show detailed information")
)

func usage() {
	fmt.Fprintf(os.Stderr, `Health Check Tool for Casbin-Mesh

Usage: %s [options]

Options:
`, os.Args[0])
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Examples:
  # Check single node health
  %s -node 192.168.1.100:4002

  # Check cluster health  
  %s -node 192.168.1.100:4002 -cluster

  # Continuous monitoring
  %s -node 192.168.1.100:4002 -cluster -watch -interval 10s

  # JSON output
  %s -node 192.168.1.100:4002 -cluster -format json

`, os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}

// checkNodeHealth checks the health of a single node
func checkNodeHealth(nodeAddr string, timeout time.Duration) (*HealthResponse, error) {
	url := fmt.Sprintf("http://%s/health", nodeAddr)

	client := &http.Client{
		Timeout: timeout,
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
func checkClusterHealth(nodeAddr string, timeout time.Duration) (*ClusterHealthStatus, error) {
	url := fmt.Sprintf("http://%s/cluster/health", nodeAddr)

	client := &http.Client{
		Timeout: timeout,
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
func printNodeHealth(health *HealthResponse, format string) {
	if format == "json" {
		output, _ := json.MarshalIndent(health, "", "  ")
		fmt.Println(string(output))
		return
	}

	// Text format
	statusIcon := "✓"
	if health.Status != "healthy" {
		statusIcon = "✗"
	}

	fmt.Printf("%s Node Health Status\n", statusIcon)
	fmt.Printf("  Node ID: %s\n", health.NodeID)
	fmt.Printf("  Status: %s\n", colorStatus(health.Status))
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
func printClusterHealth(clusterHealth *ClusterHealthStatus, format string) {
	if format == "json" {
		output, _ := json.MarshalIndent(clusterHealth, "", "  ")
		fmt.Println(string(output))
		return
	}

	// Text format
	statusIcon := "✓"
	if clusterHealth.ClusterStatus == "critical" {
		statusIcon = "✗"
	} else if clusterHealth.ClusterStatus == "degraded" {
		statusIcon = "⚠"
	}

	fmt.Printf("%s Cluster Health Status\n", statusIcon)
	fmt.Printf("  Cluster Status: %s\n", colorStatus(clusterHealth.ClusterStatus))
	fmt.Printf("  Total Nodes: %d\n", clusterHealth.TotalNodes)
	fmt.Printf("  Healthy Nodes: %d\n", clusterHealth.HealthyNodes)
	fmt.Printf("  Leader ID: %s\n", clusterHealth.LeaderID)
	fmt.Printf("  Leader Address: %s\n", clusterHealth.LeaderAddr)
	fmt.Printf("  Timestamp: %s\n\n", clusterHealth.Timestamp)

	fmt.Printf("Node Details:\n")
	for i, node := range clusterHealth.Nodes {
		nodeIcon := "✓"
		if node.Status != "healthy" {
			nodeIcon = "✗"
		}

		fmt.Printf("  %d. %s Node: %s\n", i+1, nodeIcon, node.NodeID)
		fmt.Printf("     Address: %s\n", node.Address)
		fmt.Printf("     Status: %s\n", colorStatus(node.Status))
		fmt.Printf("     Is Leader: %v\n", node.IsLeader)
		if node.Error != "" {
			fmt.Printf("     Error: %s\n", node.Error)
		}
		fmt.Printf("\n")
	}
}

// colorStatus adds color to status text for better visibility
func colorStatus(status string) string {
	switch status {
	case "healthy":
		return "\033[32m" + status + "\033[0m" // Green
	case "degraded":
		return "\033[33m" + status + "\033[0m" // Yellow
	case "unhealthy", "critical":
		return "\033[31m" + status + "\033[0m" // Red
	default:
		return status
	}
}

// runHealthCheck is the main function for health checking
func runHealthCheck(nodeAddr string, checkCluster bool, format string, timeout time.Duration) error {
	if checkCluster {
		clusterHealth, err := checkClusterHealth(nodeAddr, timeout)
		if err != nil {
			return fmt.Errorf("cluster health check failed: %v", err)
		}
		printClusterHealth(clusterHealth, format)

		// Set exit code based on cluster health
		switch clusterHealth.ClusterStatus {
		case "healthy":
			return nil
		case "degraded":
			if *verbose {
				fmt.Printf("Warning: Cluster is in degraded state\n")
			}
			os.Exit(1)
		case "critical":
			if *verbose {
				fmt.Printf("Error: Cluster is in critical state\n")
			}
			os.Exit(2)
		}
	} else {
		health, err := checkNodeHealth(nodeAddr, timeout)
		if err != nil {
			return fmt.Errorf("node health check failed: %v", err)
		}
		printNodeHealth(health, format)

		// Set exit code based on node health
		if health.Status != "healthy" {
			if *verbose {
				fmt.Printf("Warning: Node is not healthy: %s\n", health.Status)
			}
			os.Exit(1)
		}
	}

	return nil
}

func main() {
	flag.Usage = usage
	flag.Parse()

	// Handle any remaining command line arguments
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "Unknown arguments: %s\n", strings.Join(flag.Args(), " "))
		usage()
		os.Exit(1)
	}

	if *continuous {
		fmt.Printf("Starting continuous health monitoring (interval: %v)\n", *interval)
		fmt.Printf("Press Ctrl+C to stop\n\n")

		for {
			fmt.Printf("=== Health Check at %s ===\n", time.Now().Format(time.RFC3339))
			err := runHealthCheck(*nodeAddr, *checkCluster, *outputFormat, *timeout)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			}
			fmt.Printf("\n")
			time.Sleep(*interval)
		}
	} else {
		err := runHealthCheck(*nodeAddr, *checkCluster, *outputFormat, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}
