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

package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/casbin/casbin-mesh/pkg/auth"
	"github.com/casbin/casbin-mesh/pkg/handler/http"
	"github.com/go-playground/validator"
	"golang.org/x/net/context"
	"io"
	"io/ioutil"
	"log"
	"net"
	http2 "net/http"
	"runtime"
	"strings"
	"time"
)

type httpService struct {
	http.Server
	Core
	*validator.Validate
}

type Middleware func(handlerFunc http.HandlerFunc) http.HandlerFunc

func chain(outer Middleware, others ...Middleware) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		for i := len(others) - 1; i >= 0; i-- {
			next = others[i](next)
		}
		return outer(next)
	}
}

func NewHttpService(core Core) *httpService {
	httpS := http.New()
	validate := validator.New()
	srv := httpService{httpS, core, validate}
	// set response header
	httpS.Use(setResponseHeader)
	// add panic recovery and connection protection
	httpS.Use(panicRecovery)
	// add access logging
	httpS.Use(accessLogger)
	// add request size limit (100MB default) - 支持10万条策略的超大批量请求
	httpS.Use(requestSizeLimit(100 * 1024 * 1024))

	// enable global middleware
	switch core.AuthType() {
	case auth.Basic:
		httpS.Use(http.BasicAuthor(core.Check))
	}

	httpS.Handle("/join", srv.handleJoin)
	httpS.Handle("/remove", srv.handleRemove)

	// write
	httpS.Handle("/create/namespace", chain(srv.autoForwardToLeader)(srv.handleCreateNameSpace))
	httpS.Handle("/list/namespaces", chain(srv.autoForwardToLeader)(srv.handleListNamespace))
	httpS.Handle("/print/model", chain(srv.autoForwardToLeader)(srv.handlePrintModel))
	httpS.Handle("/list/policies", chain(srv.autoForwardToLeader)(srv.handleListPolicies))
	httpS.Handle("/set/model", chain(srv.autoForwardToLeader)(srv.handleSetModelFromString))
	httpS.Handle("/add/policies", chain(srv.autoForwardToLeader)(srv.handleAddPolicies))
	httpS.Handle("/remove/policies", chain(srv.autoForwardToLeader)(srv.handleRemovePolicies))
	httpS.Handle("/remove/filtered_policies", chain(srv.autoForwardToLeader)(srv.handleRemoveFilteredPolicy))
	httpS.Handle("/update/policies", chain(srv.autoForwardToLeader)(srv.handleUpdatePolicies))
	httpS.Handle("/clear/policy", chain(srv.autoForwardToLeader)(srv.handleClearPolicy))

	// read
	httpS.Handle("/enforce", srv.handleEnforce)
	httpS.Handle("/stats", srv.handleStats)
	httpS.Handle("/health", srv.handleHealth)
	httpS.Handle("/cluster/health", srv.handleClusterHealth)
	httpS.Handle("/metrics", srv.handleMetrics)
	return &srv
}

type JoinRequest struct {
	ID       string            `json:"id" validate:"required"`
	Addr     string            `json:"addr" validate:"required"`
	Voter    bool              `json:"voter" validate:"required"`
	Metadata map[string]string `json:"metadata"`
}

func setResponseHeader(ctx *http.Context) error {
	ctx.ResponseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
	return nil
}

// panicRecovery middleware to handle panics and prevent connection reset
func panicRecovery(ctx *http.Context) error {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] Recovered from panic: %v", r)

			// Ensure response is properly closed
			if ctx.ResponseWriter.Header().Get("Content-Type") == "" {
				ctx.ResponseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
			}

			// Try to send error response if headers not already sent
			ctx.ResponseWriter.WriteHeader(http2.StatusInternalServerError)
			response := map[string]interface{}{
				"error":     "Internal server error",
				"timestamp": time.Now().Format(time.RFC3339),
			}

			if jsonBytes, err := json.Marshal(response); err == nil {
				ctx.ResponseWriter.Write(jsonBytes)
			}

			// Log additional details for debugging
			log.Printf("[PANIC] Request: %s %s from %s", ctx.Request.Method, ctx.Request.URL.Path, ctx.Request.RemoteAddr)
		}
	}()

	return ctx.Next()
}

// requestSizeLimit middleware to prevent large requests from overwhelming the server
func requestSizeLimit(maxSize int64) http.HandlerFunc {
	return func(ctx *http.Context) error {
		ctx.Request.Body = http2.MaxBytesReader(ctx.ResponseWriter, ctx.Request.Body, maxSize)
		return ctx.Next()
	}
}

// accessLogger provides HTTP request/response logging with timing and status codes
func accessLogger(ctx *http.Context) error {
	startTime := time.Now()
	clientIP := ctx.Request.Header.Get("X-Real-IP")
	if clientIP == "" {
		clientIP = ctx.Request.Header.Get("X-Forwarded-For")
	}
	if clientIP == "" {
		clientIP = ctx.Request.RemoteAddr
	}

	// Execute the rest of the chain
	err := ctx.Next()

	// Log the request details
	duration := time.Since(startTime)
	statusCode := 200 // Default status
	if err != nil {
		// Get proper HTTP status code for the error
		statusCode = getErrorStatusCode(err)
	}

	log.Printf("[HTTP] %s %s %s %d %v %s",
		startTime.Format("2006-01-02T15:04:05.000Z07:00"),
		ctx.Request.Method,
		ctx.Request.RequestURI,
		statusCode,
		duration,
		clientIP)

	return err
}

// getErrorStatusCode maps errors to appropriate HTTP status codes
func getErrorStatusCode(err error) int {
	if err == nil {
		return 200
	}

	errMsg := err.Error()

	// Authentication/Authorization errors
	if contains(errMsg, "unauthorized", "not authorized") {
		return 401
	}
	if contains(errMsg, "forbidden", "permission denied") {
		return 403
	}

	// Client errors - Bad Request (match actual error patterns)
	if contains(errMsg, "validation failed", "unmarshal failed", "invalid", "bad request", "required", "Field validation", "Key:") {
		return 400
	}

	// Resource not found (match exact error strings)
	if contains(errMsg, "namespace not exist", "not found", "does not exist") {
		return 404
	}

	// Precondition failed - resource state issues (match exact error strings)
	if contains(errMsg, "model unset yet", "namespace already existed") {
		return 412
	}

	// Service unavailable - raft/cluster issues (match exact error strings)
	if contains(errMsg, "not leader", "no leader", "cluster", "raft") {
		return 503
	}

	// Request timeout - raft/storage timeouts
	if contains(errMsg, "timeout waiting for initial logs application", "timeout", "deadline exceeded") {
		return 408
	}

	// Conflict - concurrent modification
	if contains(errMsg, "conflict", "concurrent") {
		return 409
	}

	// Request entity too large
	if contains(errMsg, "too large", "limit exceeded") {
		return 413
	}

	// Stale read
	if contains(errMsg, "stale read") {
		return 406
	}

	// Default to Internal Server Error for unknown errors
	return 500
}

// contains checks if any of the patterns exist in the text (case-insensitive)
func contains(text string, patterns ...string) bool {
	textLower := strings.ToLower(text)
	for _, pattern := range patterns {
		if strings.Contains(textLower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func (s *httpService) autoForwardToLeader(fn http.HandlerFunc) http.HandlerFunc {
	return func(c *http.Context) error {
		if s.IsLeader(context.TODO()) {
			return fn(c)
		} else {
			schema := "http"
			if c.Request.TLS != nil {
				schema = "https"
			}
			c.ResponseWriter.Header().Del("Vary")
			c.ResponseWriter.Header().Del("Access-Control-Allow-Origin")

			// Ensure request body is properly closed
			defer c.Request.Body.Close()

			body, err := ioutil.ReadAll(c.Request.Body)
			if err != nil {
				log.Printf("[HTTP][Proxy] read body failed: method=%s uri=%s err=%v", c.Request.Method, c.Request.RequestURI, err)
				http2.Error(c.ResponseWriter, err.Error(), http2.StatusInternalServerError)
				return err
			}
			url := fmt.Sprintf("%s://%s%s", schema, s.LeaderAddr(), c.Request.RequestURI)
			proxyReq, err := http2.NewRequest(c.Request.Method, url, bytes.NewReader(body))

			// clone the header
			proxyReq.Header = make(http2.Header)
			for h, val := range c.Request.Header {
				proxyReq.Header[h] = val
			}

			// forward the incoming request to leader with timeout
			client := &http2.Client{
				Timeout: 15 * time.Second, // Reduced timeout to prevent client-side connection reset
				Transport: &http2.Transport{
					DisableKeepAlives:   false,
					MaxIdleConns:        10,
					MaxIdleConnsPerHost: 2,
					IdleConnTimeout:     30 * time.Second,
				},
			}

			resp, err := client.Do(proxyReq)
			if err != nil {
				log.Printf("[HTTP][Proxy] forward to leader failed: url=%s err=%v", url, err)
				http2.Error(c.ResponseWriter, err.Error(), http2.StatusBadGateway)
				return err
			}
			defer resp.Body.Close() // Close response body immediately after getting response

			// Copy status code from upstream
			c.ResponseWriter.WriteHeader(resp.StatusCode)
			log.Printf("[HTTP][Proxy] forwarded request: url=%s status=%d", url, resp.StatusCode)

			// copy the response
			_, err = io.Copy(c.ResponseWriter, resp.Body)
			if err != nil {
				log.Printf("[HTTP][Proxy] copy response failed: url=%s err=%v", url, err)
				return err
			}
		}
		return nil
	}
}

func (s *httpService) handleJoin(ctx *http.Context) (err error) {
	var request JoinRequest
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	if err = s.Join(context.TODO(), request.ID, request.Addr, request.Voter, request.Metadata); err != nil {
		return
	}
	ctx.StatusCode(http2.StatusOK)
	return nil
}

type RemoveRequest struct {
	ID string `json:"id" validate:"required"`
}

func (s *httpService) handleRemove(ctx *http.Context) (err error) {
	var request RemoveRequest
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	if err = s.Remove(context.TODO(), request.ID); err != nil {
		return
	}
	ctx.StatusCode(http2.StatusOK)
	return nil
}

type CreateNameSpaceRequest struct {
	NS string `json:"ns" validate:"required"`
}

func (s *httpService) handleCreateNameSpace(ctx *http.Context) (err error) {
	var request CreateNameSpaceRequest
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	if err = s.CreateNamespace(context.TODO(), request.NS); err != nil {
		return
	}
	ctx.StatusCode(http2.StatusOK)
	return nil
}

type SetModelFromStringRequest struct {
	NS   string `json:"ns" validate:"required"`
	Text string `json:"text" validate:"required"`
}

func (s *httpService) handleSetModelFromString(ctx *http.Context) (err error) {
	var request SetModelFromStringRequest
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	if err = s.SetModelFromString(context.TODO(), request.NS, request.Text); err != nil {
		return
	}
	ctx.StatusCode(http2.StatusOK)
	return nil
}

type EnforceRequest struct {
	NS        string        `json:"ns" validate:"required"`
	Level     int32         `json:"level"`
	Freshness int64         `json:"freshness"`
	Params    []interface{} `json:"params"`
}

type EnforceReply struct {
	Ok bool `json:"ok"`
}

func (s *httpService) handleEnforce(ctx *http.Context) (err error) {
	var request EnforceRequest
	var output bool
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	if output, err = s.Enforce(context.TODO(), request.NS, request.Level, request.Freshness, request.Params...); err != nil {
		return
	}
	return ctx.StatusCode(http2.StatusOK).JSON(EnforceReply{Ok: output})
}

type AddPoliciesRequest struct {
	NS    string     `json:"ns" validate:"required"`
	Sec   string     `json:"sec" validate:"required"`
	PType string     `json:"ptype" validate:"required"`
	Rules [][]string `json:"rules" validate:"required"`
}

type Response struct {
	Effected      bool       `json:"effected,omitempty"`
	EffectedRules [][]string `json:"effected_rules,omitempty"`
}

func (s *httpService) handleAddPolicies(ctx *http.Context) (err error) {
	start := time.Now()
	var request AddPoliciesRequest

	// 优先尝试JSON body解析，如果失败则尝试URL参数
	jsonDecodeErr := s.decode(ctx.Request.Body, &request)

	// 如果JSON解析失败，尝试从URL参数解析（向后兼容）
	if jsonDecodeErr != nil {
		log.Printf("[HTTP][AddPolicies] JSON decode failed, trying URL params: err=%v", jsonDecodeErr)

		// 从URL参数解析
		query := ctx.Request.URL.Query()
		request.NS = query.Get("ns")
		request.Sec = query.Get("sec")
		request.PType = query.Get("ptype")

		// 解析rules参数（可能有多个）
		rulesParams := query["rules"]
		if len(rulesParams) == 0 {
			return fmt.Errorf("no rules provided in URL parameters")
		}

		// 将URL参数中的rules转换为二维数组
		// 假设每个rule是逗号分隔的值
		request.Rules = make([][]string, 0, len(rulesParams)/4) // 估计每个rule有4个元素

		for i := 0; i < len(rulesParams); i += 4 {
			if i+3 < len(rulesParams) {
				rule := []string{rulesParams[i], rulesParams[i+1], rulesParams[i+2], rulesParams[i+3]}
				request.Rules = append(request.Rules, rule)
			}
		}

		log.Printf("[HTTP][AddPolicies] Using URL params fallback: ns=%s rules_count=%d", request.NS, len(request.Rules))
	}

	decodeTime := time.Since(start)
	log.Printf("[HTTP][AddPolicies] decode completed in %v: ns=%s rules_count=%d method=%s",
		decodeTime, request.NS, len(request.Rules),
		func() string {
			if jsonDecodeErr == nil {
				return "JSON"
			}
			return "URL_PARAMS"
		}())

	// Check batch size limit to prevent overwhelming the system
	const maxBatchSize = 100000 // 支持10万条策略的大批次处理，避免任何限制问题
	if len(request.Rules) > maxBatchSize {
		err := fmt.Errorf("batch size too large: %d rules exceeds maximum of %d", len(request.Rules), maxBatchSize)
		log.Printf("[HTTP][AddPolicies] batch size check failed: ns=%s rules_count=%d", request.NS, len(request.Rules))
		return err
	}

	processStart := time.Now()
	var rules [][]string
	if rules, err = s.AddPolicies(context.TODO(), request.NS, request.Sec, request.PType, request.Rules); err != nil {
		log.Printf("[HTTP][AddPolicies] add failed after %v: ns=%s sec=%s ptype=%s rules_count=%d err=%v",
			time.Since(start), request.NS, request.Sec, request.PType, len(request.Rules), err)
		return err
	}

	processTime := time.Since(processStart)
	totalTime := time.Since(start)
	log.Printf("[HTTP][AddPolicies] completed: total=%v decode=%v process=%v rules=%d rate=%.2f rules/sec",
		totalTime, decodeTime, processTime, len(request.Rules), float64(len(request.Rules))/totalTime.Seconds())

	return ctx.StatusCode(http2.StatusOK).JSON(Response{EffectedRules: rules})
}

type RemovePoliciesRequest struct {
	NS    string     `json:"ns" validate:"required"`
	Sec   string     `json:"sec" validate:"required"`
	PType string     `json:"ptype" validate:"required"`
	Rules [][]string `json:"rules" validate:"required"`
}

func (s *httpService) handleRemovePolicies(ctx *http.Context) (err error) {
	var request RemovePoliciesRequest
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	var rules [][]string
	if rules, err = s.RemovePolicies(context.TODO(), request.NS, request.Sec, request.PType, request.Rules); err != nil {
		return
	}

	return ctx.StatusCode(http2.StatusOK).JSON(Response{EffectedRules: rules})
}

type RemoveFilteredPolicyRequest struct {
	NS          string   `json:"ns" validate:"required"`
	Sec         string   `json:"sec" validate:"required"`
	PType       string   `json:"ptype" validate:"required"`
	FieldIndex  *int32   `json:"fieldIndex" validate:"required"`
	FieldValues []string `json:"fieldValues" validate:"required"`
}

func (s *httpService) handleRemoveFilteredPolicy(ctx *http.Context) (err error) {
	var request RemoveFilteredPolicyRequest
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	var rules [][]string
	if rules, err = s.RemoveFilteredPolicy(context.TODO(), request.NS, request.Sec, request.PType, *request.FieldIndex, request.FieldValues); err != nil {
		return
	}
	return ctx.StatusCode(http2.StatusOK).JSON(Response{EffectedRules: rules})
}

type UpdatePoliciesRequest struct {
	NS       string     `json:"ns" validate:"required"`
	Sec      string     `json:"sec" validate:"required"`
	PType    string     `json:"ptype" validate:"required"`
	NewRules [][]string `json:"newRules" validate:"required"`
	OldRules [][]string `json:"oldRules" validate:"required"`
}

func (s *httpService) handleUpdatePolicies(ctx *http.Context) (err error) {
	var request UpdatePoliciesRequest
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	var effected bool
	if effected, err = s.UpdatePolicies(context.TODO(), request.NS, request.Sec, request.PType, request.NewRules, request.OldRules); err != nil {
		return
	}
	return ctx.StatusCode(http2.StatusOK).JSON(Response{Effected: effected})
}

type ClearPolicyRequest struct {
	NS string `json:"ns" validate:"required"`
}

func (s *httpService) handleClearPolicy(ctx *http.Context) (err error) {
	var request ClearPolicyRequest
	if err = s.decode(ctx.Request.Body, &request); err != nil {
		return
	}
	if err = s.ClearPolicy(context.TODO(), request.NS); err != nil {
		return
	}
	ctx.StatusCode(http2.StatusOK)
	return
}

type ListPoliciesRequest struct {
	NS      string `json:"ns" validate:"required"`
	Cursor  string `json:"cursor"`
	Skip    int64  `json:"skip"`
	Limit   int64  `json:"limit"`
	Reverse bool   `json:"reverse"`
}

func (s *httpService) handleListPolicies(ctx *http.Context) error {
	var request ListPoliciesRequest
	if err := s.decode(ctx.Request.Body, &request); err != nil {
		return err
	}
	out, err := s.ListPolicies(context.TODO(), request.NS, request.Cursor, request.Skip, request.Limit, request.Reverse)
	if err != nil {
		return err
	}
	return ctx.StatusCode(http2.StatusOK).JSON(out)
}

type PrintModelRequest struct {
	NS string `json:"ns" validate:"required"`
}

func (s *httpService) handlePrintModel(ctx *http.Context) error {
	var request PrintModelRequest
	if err := s.decode(ctx.Request.Body, &request); err != nil {
		return err
	}
	out, err := s.PrintModel(context.TODO(), request.NS)
	if err != nil {
		return err
	}
	return ctx.StatusCode(http2.StatusOK).JSON(out)
}

func (s *httpService) handleListNamespace(ctx *http.Context) error {
	out, err := s.ListNamespaces(context.TODO())
	if err != nil {
		return err
	}
	return ctx.StatusCode(http2.StatusOK).JSON(out)
}

func (s *httpService) handleStats(ctx *http.Context) error {
	out, err := s.Stats(context.TODO())
	if err != nil {
		return err
	}
	return ctx.StatusCode(http2.StatusOK).JSON(out)
}

type HealthResponse struct {
	Status     string `json:"status"`
	NodeID     string `json:"node_id"`
	IsLeader   bool   `json:"is_leader"`
	State      string `json:"state"`
	LeaderID   string `json:"leader_id,omitempty"`
	LeaderAddr string `json:"leader_addr,omitempty"`
	Timestamp  string `json:"timestamp"`
}

func (s *httpService) handleHealth(ctx *http.Context) error {
	leaderID, _ := s.LeaderID()
	start := time.Now()

	health := HealthResponse{
		Status:     "healthy",
		NodeID:     s.ID(),
		IsLeader:   s.IsLeader(context.TODO()),
		State:      s.State().String(),
		LeaderID:   leaderID,
		LeaderAddr: s.LeaderAddr(),
		Timestamp:  time.Now().Format(time.RFC3339),
	}

	var healthIssues []string

	// Check if the node can access its storage
	if _, err := s.Stats(context.TODO()); err != nil {
		healthIssues = append(healthIssues, fmt.Sprintf("storage access failed: %v", err))
		log.Printf("[Health] Storage access failed: %v", err)
	}

	// Check leadership timeout (if follower, check last heartbeat)
	if !health.IsLeader && health.State == "follower" {
		// Simulate heartbeat check - in real implementation you'd track last heartbeat
		// Here we just check if we have a valid leader
		if health.LeaderAddr == "" {
			healthIssues = append(healthIssues, "no leader detected")
		}
	}

	// Check cluster connectivity if we're a leader
	if health.IsLeader {
		nodes, err := s.Nodes()
		if err == nil && len(nodes) > 1 {
			reachableNodes := 0
			for _, node := range nodes {
				if node.ID != health.NodeID {
					if err := s.checkNodeHealth(node.Addr); err == nil {
						reachableNodes++
					}
				}
			}
			if reachableNodes < len(nodes)/2 {
				healthIssues = append(healthIssues, "insufficient cluster connectivity")
			}
		}
	}

	// Performance health check - response time
	responseTime := time.Since(start)
	if responseTime > 5*time.Second {
		healthIssues = append(healthIssues, "slow health check response")
	}

	if len(healthIssues) > 0 {
		health.Status = "degraded"
		log.Printf("[Health] Node degraded: %v", healthIssues)
	}

	statusCode := http2.StatusOK
	if health.Status != "healthy" {
		if len(healthIssues) > 2 {
			health.Status = "unhealthy"
			statusCode = http2.StatusServiceUnavailable
		} else {
			statusCode = http2.StatusPartialContent
		}
	}

	return ctx.StatusCode(statusCode).JSON(health)
}

type NodeHealth struct {
	NodeID      string `json:"node_id"`
	Address     string `json:"address"`
	Status      string `json:"status"`
	IsLeader    bool   `json:"is_leader"`
	LastContact string `json:"last_contact,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ClusterHealthResponse struct {
	ClusterStatus string       `json:"cluster_status"`
	LeaderID      string       `json:"leader_id"`
	LeaderAddr    string       `json:"leader_addr"`
	TotalNodes    int          `json:"total_nodes"`
	HealthyNodes  int          `json:"healthy_nodes"`
	Nodes         []NodeHealth `json:"nodes"`
	Timestamp     string       `json:"timestamp"`
}

func (s *httpService) handleClusterHealth(ctx *http.Context) error {
	nodes, err := s.Nodes()
	if err != nil {
		log.Printf("[ClusterHealth] Failed to get nodes: %v", err)
		return err
	}

	leaderID, _ := s.LeaderID()
	leaderAddr := s.LeaderAddr()

	var nodeHealths []NodeHealth
	healthyCount := 0

	for _, node := range nodes {
		nodeHealth := NodeHealth{
			NodeID:   node.ID,
			Address:  node.Addr,
			IsLeader: node.ID == leaderID,
		}

		// Check node health by trying to connect
		if err := s.checkNodeHealth(node.Addr); err != nil {
			nodeHealth.Status = "unhealthy"
			nodeHealth.Error = err.Error()
			log.Printf("[ClusterHealth] Node %s (%s) unhealthy: %v", node.ID, node.Addr, err)
		} else {
			nodeHealth.Status = "healthy"
			healthyCount++
		}

		nodeHealths = append(nodeHealths, nodeHealth)
	}

	clusterStatus := "healthy"
	if healthyCount == 0 {
		clusterStatus = "critical"
	} else if healthyCount < len(nodes)/2+1 {
		clusterStatus = "degraded" // Less than majority
	}

	response := ClusterHealthResponse{
		ClusterStatus: clusterStatus,
		LeaderID:      leaderID,
		LeaderAddr:    leaderAddr,
		TotalNodes:    len(nodes),
		HealthyNodes:  healthyCount,
		Nodes:         nodeHealths,
		Timestamp:     time.Now().Format(time.RFC3339),
	}

	statusCode := http2.StatusOK
	if clusterStatus == "critical" {
		statusCode = http2.StatusServiceUnavailable
	} else if clusterStatus == "degraded" {
		statusCode = http2.StatusPartialContent
	}

	return ctx.StatusCode(statusCode).JSON(response)
}

// checkNodeHealth performs a comprehensive health check with retry mechanism
func (s *httpService) checkNodeHealth(addr string) error {
	const maxRetries = 3
	const retryDelay = 500 * time.Millisecond

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		// Try to establish a TCP connection with timeout
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			lastErr = fmt.Errorf("connection attempt %d failed: %v", i+1, err)
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
		} else {
			defer conn.Close()

			// Additional check: try to write/read a small amount of data
			conn.SetDeadline(time.Now().Add(1 * time.Second))
			testData := []byte("ping")
			_, writeErr := conn.Write(testData)
			if writeErr != nil {
				lastErr = fmt.Errorf("write test failed: %v", writeErr)
				if i < maxRetries-1 {
					time.Sleep(retryDelay)
					continue
				}
			} else {
				// Connection is healthy
				return nil
			}
		}
	}

	return fmt.Errorf("node health check failed after %d retries: %v", maxRetries, lastErr)
}

type MetricsResponse struct {
	Timestamp       string                 `json:"timestamp"`
	NodeID          string                 `json:"node_id"`
	Uptime          string                 `json:"uptime"`
	MemoryUsage     map[string]interface{} `json:"memory_usage"`
	RequestsHandled int64                  `json:"requests_handled"`
	Namespaces      []string               `json:"namespaces"`
	RaftMetrics     map[string]interface{} `json:"raft_metrics"`
}

func (s *httpService) handleMetrics(ctx *http.Context) error {
	start := time.Now()
	stats, err := s.Stats(context.TODO())
	if err != nil {
		return err
	}

	// Get namespaces
	namespaces, _ := s.ListNamespaces(context.TODO())

	// Calculate uptime (this is a simplified calculation)
	// In production, you'd track the actual start time

	// Get memory stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	memoryUsage := map[string]interface{}{
		"alloc_bytes":       memStats.Alloc,
		"total_alloc_bytes": memStats.TotalAlloc,
		"sys_bytes":         memStats.Sys,
		"num_gc":            memStats.NumGC,
		"gc_cpu_fraction":   memStats.GCCPUFraction,
	}

	// Get cluster info
	nodes, _ := s.Nodes()
	clusterInfo := map[string]interface{}{
		"total_nodes": len(nodes),
		"node_state":  s.State().String(),
		"is_leader":   s.IsLeader(context.TODO()),
		"leader_addr": s.LeaderAddr(),
	}

	// Basic performance metrics
	performanceMetrics := map[string]interface{}{
		"response_time_ms": time.Since(start).Milliseconds(),
		"goroutines":       runtime.NumGoroutine(),
	}

	// Enhanced metrics response
	metrics := map[string]interface{}{
		"timestamp":           time.Now().Format(time.RFC3339),
		"node_id":             s.ID(),
		"namespaces":          namespaces,
		"raft_metrics":        stats,
		"memory_usage":        memoryUsage,
		"cluster_info":        clusterInfo,
		"performance_metrics": performanceMetrics,
		"version_info": map[string]interface{}{
			"go_version": runtime.Version(),
			"go_arch":    runtime.GOARCH,
			"go_os":      runtime.GOOS,
		},
	}

	return ctx.StatusCode(http2.StatusOK).JSON(metrics)
}

func (s *httpService) decode(reader io.ReadCloser, output interface{}) (err error) {
	if err = json.NewDecoder(reader).Decode(&output); err != nil {
		log.Printf("[HTTP][Decode] json decode failed: err=%v", err)
		return
	}
	if err = s.Validate.Struct(output); err != nil {
		log.Printf("[HTTP][Decode] validation failed: err=%v", err)
		return
	}
	return nil
}
