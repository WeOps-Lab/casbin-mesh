#!/bin/bash

# Casbin-Mesh Cluster Monitoring Script
# This script continuously monitors cluster health and logs issues

# Configuration
CLUSTER_NODES=${CLUSTER_NODES:-"127.0.0.1:8080,127.0.0.1:8081,127.0.0.1:8082"}
CHECK_INTERVAL=${CHECK_INTERVAL:-30}
LOG_FILE=${LOG_FILE:-"/tmp/casbin-mesh-monitor.log"}
ALERT_THRESHOLD=${ALERT_THRESHOLD:-2}  # Number of consecutive failures before alert

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Counter for consecutive failures
consecutive_failures=0

log_message() {
    local level=$1
    local message=$2
    local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
    echo -e "[$timestamp] [$level] $message" | tee -a "$LOG_FILE"
}

check_cluster_health() {
    local node=$1
    local temp_file=$(mktemp)
    
    # Try to get cluster health
    if curl -s --max-time 10 "http://$node/cluster/health" > "$temp_file" 2>/dev/null; then
        local status=$(jq -r '.cluster_status // "unknown"' "$temp_file" 2>/dev/null || echo "unknown")
        local healthy_nodes=$(jq -r '.healthy_nodes // 0' "$temp_file" 2>/dev/null || echo 0)
        local total_nodes=$(jq -r '.total_nodes // 0' "$temp_file" 2>/dev/null || echo 0)
        
        case "$status" in
            "healthy")
                log_message "INFO" "${GREEN}✓ Cluster is healthy ($healthy_nodes/$total_nodes nodes)${NC}"
                consecutive_failures=0
                ;;
            "degraded")
                log_message "WARN" "${YELLOW}⚠ Cluster is degraded ($healthy_nodes/$total_nodes nodes)${NC}"
                ((consecutive_failures++))
                ;;
            "critical")
                log_message "ERROR" "${RED}✗ Cluster is critical ($healthy_nodes/$total_nodes nodes)${NC}"
                ((consecutive_failures++))
                ;;
            *)
                log_message "ERROR" "${RED}✗ Unknown cluster status: $status${NC}"
                ((consecutive_failures++))
                ;;
        esac
        
        # Show node details in verbose mode
        if [ "$VERBOSE" = "true" ]; then
            echo "Node details:"
            jq -r '.nodes[]? | "  - \(.node_id): \(.status) (\(.address))"' "$temp_file" 2>/dev/null || echo "  - Unable to parse node details"
        fi
        
    else
        log_message "ERROR" "${RED}✗ Failed to connect to cluster node $node${NC}"
        ((consecutive_failures++))
    fi
    
    rm -f "$temp_file"
}

send_alert() {
    local message="$1"
    log_message "ALERT" "🚨 $message"
    
    # Add your alerting logic here
    # Examples:
    # - Send email
    # - Post to Slack webhook
    # - Send to monitoring system
    
    # Example webhook call (uncomment and configure as needed):
    # curl -X POST -H 'Content-type: application/json' \
    #     --data "{\"text\":\"Casbin-Mesh Alert: $message\"}" \
    #     "$SLACK_WEBHOOK_URL"
}

usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -n, --nodes NODES        Comma-separated list of cluster nodes (default: $CLUSTER_NODES)"
    echo "  -i, --interval SECONDS   Check interval in seconds (default: $CHECK_INTERVAL)"
    echo "  -l, --log-file FILE      Log file path (default: $LOG_FILE)"
    echo "  -t, --threshold COUNT    Alert threshold for consecutive failures (default: $ALERT_THRESHOLD)"
    echo "  -v, --verbose           Show detailed node information"
    echo "  -h, --help              Show this help message"
    echo ""
    echo "Environment Variables:"
    echo "  CLUSTER_NODES           Override default cluster nodes"
    echo "  CHECK_INTERVAL          Override default check interval"
    echo "  LOG_FILE                Override default log file"
    echo "  ALERT_THRESHOLD         Override default alert threshold"
    echo "  SLACK_WEBHOOK_URL       Slack webhook URL for alerts"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -n|--nodes)
            CLUSTER_NODES="$2"
            shift 2
            ;;
        -i|--interval)
            CHECK_INTERVAL="$2"
            shift 2
            ;;
        -l|--log-file)
            LOG_FILE="$2"
            shift 2
            ;;
        -t|--threshold)
            ALERT_THRESHOLD="$2"
            shift 2
            ;;
        -v|--verbose)
            VERBOSE="true"
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Validate dependencies
if ! command -v curl &> /dev/null; then
    echo "Error: curl is required but not installed."
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo "Warning: jq is not installed. JSON parsing will be limited."
fi

# Convert nodes string to array
IFS=',' read -ra NODES <<< "$CLUSTER_NODES"

# Create log file directory if it doesn't exist
mkdir -p "$(dirname "$LOG_FILE")"

log_message "INFO" "Starting Casbin-Mesh cluster monitoring"
log_message "INFO" "Nodes: ${NODES[*]}"
log_message "INFO" "Check interval: ${CHECK_INTERVAL}s"
log_message "INFO" "Alert threshold: $ALERT_THRESHOLD consecutive failures"
log_message "INFO" "Log file: $LOG_FILE"

# Trap signals for graceful shutdown
trap 'log_message "INFO" "Monitoring stopped"; exit 0' INT TERM

# Main monitoring loop
while true; do
    # Try each node until we get a successful response
    cluster_checked=false
    
    for node in "${NODES[@]}"; do
        if [ "$cluster_checked" = "false" ]; then
            check_cluster_health "$node"
            cluster_checked=true
            break
        fi
    done
    
    # Send alert if threshold is reached
    if [ $consecutive_failures -ge $ALERT_THRESHOLD ]; then
        send_alert "Cluster has failed health checks $consecutive_failures consecutive times"
        consecutive_failures=0  # Reset to avoid spamming
    fi
    
    sleep "$CHECK_INTERVAL"
done