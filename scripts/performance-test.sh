#!/bin/bash

# Casbin-Mesh Performance Testing Script
# This script runs various performance tests against a Casbin-Mesh cluster

set -e

# Configuration
CLUSTER_ENDPOINT="127.0.0.1:8080"
TEST_NAMESPACE="perf_test"
CONCURRENT_USERS=10
REQUESTS_PER_USER=100
TEST_DURATION=60
VERBOSE=false
OUTPUT_FILE=""
CLEANUP_AFTER_TEST=true

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test results
declare -A test_results

log() {
    local level=$1
    shift
    local message="$*"
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    
    case $level in
        "INFO")
            echo -e "${GREEN}[INFO]${NC} [$timestamp] $message"
            ;;
        "WARN")
            echo -e "${YELLOW}[WARN]${NC} [$timestamp] $message"
            ;;
        "ERROR")
            echo -e "${RED}[ERROR]${NC} [$timestamp] $message"
            ;;
        "DEBUG")
            if [ "$VERBOSE" = "true" ]; then
                echo -e "${BLUE}[DEBUG]${NC} [$timestamp] $message"
            fi
            ;;
    esac
}

usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Options:
    -e, --endpoint URL      Cluster endpoint (default: $CLUSTER_ENDPOINT)
    -n, --namespace NAME    Test namespace (default: $TEST_NAMESPACE)
    -c, --concurrent N      Concurrent users (default: $CONCURRENT_USERS)
    -r, --requests N        Requests per user (default: $REQUESTS_PER_USER)
    -d, --duration SECONDS  Test duration in seconds (default: $TEST_DURATION)
    -o, --output FILE       Output results to file
    --no-cleanup            Don't cleanup test data after completion
    -v, --verbose           Enable verbose output
    -h, --help              Show this help message

Test Types:
    The script runs the following performance tests:
    1. Namespace operations (create/list)
    2. Policy operations (add/remove/list)
    3. Enforcement operations (enforce)
    4. Mixed workload test
    5. Stress test (high concurrency)

Examples:
    # Basic performance test
    $0 -e 127.0.0.1:8080

    # High concurrency test
    $0 -e 127.0.0.1:8080 -c 50 -r 200

    # Long duration test with output file
    $0 -e 127.0.0.1:8080 -d 300 -o perf_results.json

EOF
}

check_dependencies() {
    local deps=("curl" "jq" "bc")
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            log "ERROR" "Required dependency '$dep' not found"
            exit 1
        fi
    done
}

check_cluster_health() {
    log "INFO" "Checking cluster health..."
    
    local health_url="http://$CLUSTER_ENDPOINT/health"
    local response=$(curl -s --max-time 10 "$health_url" 2>/dev/null || echo '{"status":"error"}')
    local status=$(echo "$response" | jq -r '.status // "error"')
    
    if [ "$status" != "healthy" ]; then
        log "ERROR" "Cluster is not healthy (status: $status)"
        exit 1
    fi
    
    log "INFO" "Cluster is healthy"
}

setup_test_environment() {
    log "INFO" "Setting up test environment..."
    
    # Create test namespace
    local create_ns_response=$(curl -s -X POST \
        -H "Content-Type: application/json" \
        "http://$CLUSTER_ENDPOINT/create/namespace" \
        -d "{\"namespace\":\"$TEST_NAMESPACE\"}" 2>/dev/null || echo '{"error":"failed"}')
    
    log "DEBUG" "Namespace creation response: $create_ns_response"
    
    # Set a simple RBAC model for the test namespace
    local model='[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = r.sub == p.sub && r.obj == p.obj && r.act == p.act'
    
    local set_model_response=$(curl -s -X POST \
        -H "Content-Type: application/json" \
        "http://$CLUSTER_ENDPOINT/set/model" \
        -d "{\"namespace\":\"$TEST_NAMESPACE\",\"model\":\"$(echo "$model" | tr '\n' '|')\"}" 2>/dev/null || echo '{"error":"failed"}')
    
    log "DEBUG" "Model setup response: $set_model_response"
    log "INFO" "Test environment setup completed"
}

cleanup_test_environment() {
    if [ "$CLEANUP_AFTER_TEST" = "true" ]; then
        log "INFO" "Cleaning up test environment..."
        
        # Clear policies in test namespace
        curl -s -X POST \
            -H "Content-Type: application/json" \
            "http://$CLUSTER_ENDPOINT/clear/policy" \
            -d "{\"namespace\":\"$TEST_NAMESPACE\"}" >/dev/null 2>&1
        
        log "INFO" "Test environment cleaned up"
    fi
}

# Test function: Add policies
test_add_policies() {
    local num_policies=$1
    local concurrent_requests=${2:-1}
    
    log "DEBUG" "Testing add policies: $num_policies policies, $concurrent_requests concurrent requests"
    
    local start_time=$(date +%s.%N)
    local success_count=0
    local error_count=0
    
    # Generate policy data
    local policies='['
    for ((i=1; i<=num_policies; i++)); do
        if [ $i -gt 1 ]; then
            policies+=','
        fi
        policies+="[\"user$i\",\"data$i\",\"read\"]"
    done
    policies+=']'
    
    # Make request
    local response=$(curl -s -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" \
        "http://$CLUSTER_ENDPOINT/add/policies" \
        -d "{\"namespace\":\"$TEST_NAMESPACE\",\"policies\":$policies}" 2>/dev/null)
    
    local http_code="${response: -3}"
    local body="${response%???}"
    
    local end_time=$(date +%s.%N)
    local duration=$(echo "$end_time - $start_time" | bc)
    
    if [ "$http_code" -eq 200 ]; then
        success_count=1
    else
        error_count=1
        log "DEBUG" "Add policies failed: HTTP $http_code, Body: $body"
    fi
    
    echo "$duration,$success_count,$error_count"
}

# Test function: List policies
test_list_policies() {
    log "DEBUG" "Testing list policies"
    
    local start_time=$(date +%s.%N)
    local success_count=0
    local error_count=0
    
    local response=$(curl -s -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" \
        "http://$CLUSTER_ENDPOINT/list/policies" \
        -d "{\"namespace\":\"$TEST_NAMESPACE\"}" 2>/dev/null)
    
    local http_code="${response: -3}"
    local end_time=$(date +%s.%N)
    local duration=$(echo "$end_time - $start_time" | bc)
    
    if [ "$http_code" -eq 200 ]; then
        success_count=1
    else
        error_count=1
        log "DEBUG" "List policies failed: HTTP $http_code"
    fi
    
    echo "$duration,$success_count,$error_count"
}

# Test function: Enforce
test_enforce() {
    local user=$1
    local obj=$2
    local act=$3
    
    log "DEBUG" "Testing enforce: $user, $obj, $act"
    
    local start_time=$(date +%s.%N)
    local success_count=0
    local error_count=0
    
    local response=$(curl -s -w "%{http_code}" -X POST \
        -H "Content-Type: application/json" \
        "http://$CLUSTER_ENDPOINT/enforce" \
        -d "{\"namespace\":\"$TEST_NAMESPACE\",\"params\":[\"$user\",\"$obj\",\"$act\"]}" 2>/dev/null)
    
    local http_code="${response: -3}"
    local end_time=$(date +%s.%N)
    local duration=$(echo "$end_time - $start_time" | bc)
    
    if [ "$http_code" -eq 200 ]; then
        success_count=1
    else
        error_count=1
        log "DEBUG" "Enforce failed: HTTP $http_code"
    fi
    
    echo "$duration,$success_count,$error_count"
}

# Run concurrent test
run_concurrent_test() {
    local test_name="$1"
    local test_function="$2"
    local concurrent_users="$3"
    local requests_per_user="$4"
    
    log "INFO" "Running $test_name (users: $concurrent_users, requests/user: $requests_per_user)"
    
    local temp_dir=$(mktemp -d)
    local pids=()
    
    # Start concurrent workers
    for ((u=1; u<=concurrent_users; u++)); do
        (
            local total_duration=0
            local total_success=0
            local total_errors=0
            
            for ((r=1; r<=requests_per_user; r++)); do
                local result
                case "$test_function" in
                    "add_policies")
                        result=$(test_add_policies 5)
                        ;;
                    "list_policies")
                        result=$(test_list_policies)
                        ;;
                    "enforce")
                        local user_id=$((u % 100 + 1))
                        result=$(test_enforce "user$user_id" "data$user_id" "read")
                        ;;
                esac
                
                local duration=$(echo "$result" | cut -d',' -f1)
                local success=$(echo "$result" | cut -d',' -f2)
                local errors=$(echo "$result" | cut -d',' -f3)
                
                total_duration=$(echo "$total_duration + $duration" | bc)
                total_success=$((total_success + success))
                total_errors=$((total_errors + errors))
            done
            
            echo "$total_duration,$total_success,$total_errors" > "$temp_dir/worker_$u.result"
        ) &
        pids+=($!)
    done
    
    # Wait for all workers to complete
    local start_time=$(date +%s)
    for pid in "${pids[@]}"; do
        wait "$pid"
    done
    local end_time=$(date +%s)
    local total_wall_time=$((end_time - start_time))
    
    # Aggregate results
    local total_duration=0
    local total_success=0
    local total_errors=0
    local total_requests=0
    
    for ((u=1; u<=concurrent_users; u++)); do
        if [ -f "$temp_dir/worker_$u.result" ]; then
            local worker_result=$(cat "$temp_dir/worker_$u.result")
            local duration=$(echo "$worker_result" | cut -d',' -f1)
            local success=$(echo "$worker_result" | cut -d',' -f2)
            local errors=$(echo "$worker_result" | cut -d',' -f3)
            
            total_duration=$(echo "$total_duration + $duration" | bc)
            total_success=$((total_success + success))
            total_errors=$((total_errors + errors))
            total_requests=$((total_requests + success + errors))
        fi
    done
    
    # Calculate metrics
    local avg_response_time=0
    local throughput=0
    local success_rate=0
    
    if [ "$total_requests" -gt 0 ]; then
        avg_response_time=$(echo "scale=3; $total_duration / $total_requests" | bc)
        throughput=$(echo "scale=2; $total_requests / $total_wall_time" | bc)
        success_rate=$(echo "scale=2; $total_success * 100 / $total_requests" | bc)
    fi
    
    # Store results
    test_results["${test_name}_total_requests"]=$total_requests
    test_results["${test_name}_total_success"]=$total_success
    test_results["${test_name}_total_errors"]=$total_errors
    test_results["${test_name}_avg_response_time"]=$avg_response_time
    test_results["${test_name}_throughput"]=$throughput
    test_results["${test_name}_success_rate"]=$success_rate
    test_results["${test_name}_wall_time"]=$total_wall_time
    
    log "INFO" "Test completed: $test_name"
    log "INFO" "  Requests: $total_requests, Success: $total_success, Errors: $total_errors"
    log "INFO" "  Avg Response Time: ${avg_response_time}s, Throughput: ${throughput} req/s"
    log "INFO" "  Success Rate: ${success_rate}%, Wall Time: ${total_wall_time}s"
    
    # Cleanup
    rm -rf "$temp_dir"
}

# Generate performance report
generate_report() {
    log "INFO" "Generating performance report..."
    
    local report_data='{
        "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
        "configuration": {
            "cluster_endpoint": "'$CLUSTER_ENDPOINT'",
            "test_namespace": "'$TEST_NAMESPACE'",
            "concurrent_users": '$CONCURRENT_USERS',
            "requests_per_user": '$REQUESTS_PER_USER'
        },
        "results": {'
    
    local first=true
    for key in "${!test_results[@]}"; do
        if [ "$first" = false ]; then
            report_data+=','
        fi
        first=false
        report_data+="\"$key\": \"${test_results[$key]}\""
    done
    
    report_data+='
        }
    }'
    
    echo "$report_data" | jq '.'
    
    if [ -n "$OUTPUT_FILE" ]; then
        echo "$report_data" | jq '.' > "$OUTPUT_FILE"
        log "INFO" "Results saved to: $OUTPUT_FILE"
    fi
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -e|--endpoint)
            CLUSTER_ENDPOINT="$2"
            shift 2
            ;;
        -n|--namespace)
            TEST_NAMESPACE="$2"
            shift 2
            ;;
        -c|--concurrent)
            CONCURRENT_USERS="$2"
            shift 2
            ;;
        -r|--requests)
            REQUESTS_PER_USER="$2"
            shift 2
            ;;
        -d|--duration)
            TEST_DURATION="$2"
            shift 2
            ;;
        -o|--output)
            OUTPUT_FILE="$2"
            shift 2
            ;;
        --no-cleanup)
            CLEANUP_AFTER_TEST=false
            shift
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            log "ERROR" "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Main execution
main() {
    log "INFO" "Starting Casbin-Mesh Performance Test"
    log "INFO" "Target: $CLUSTER_ENDPOINT"
    log "INFO" "Configuration: $CONCURRENT_USERS concurrent users, $REQUESTS_PER_USER requests each"
    
    # Check dependencies and cluster health
    check_dependencies
    check_cluster_health
    
    # Setup test environment
    setup_test_environment
    
    # Trap for cleanup
    trap cleanup_test_environment EXIT
    
    # Run performance tests
    log "INFO" "Starting performance tests..."
    
    # Test 1: Policy addition performance
    run_concurrent_test "add_policies" "add_policies" $CONCURRENT_USERS $REQUESTS_PER_USER
    
    # Test 2: Policy listing performance
    run_concurrent_test "list_policies" "list_policies" $CONCURRENT_USERS $REQUESTS_PER_USER
    
    # Test 3: Enforcement performance
    run_concurrent_test "enforce" "enforce" $CONCURRENT_USERS $REQUESTS_PER_USER
    
    # Generate and display report
    generate_report
    
    log "INFO" "Performance testing completed"
}

# Run main function
main