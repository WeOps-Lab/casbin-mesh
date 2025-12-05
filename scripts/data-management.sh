#!/bin/bash

# Casbin-Mesh Data Management Script
# This script provides utilities for managing Casbin-Mesh data directories

set -e

# Configuration
DATA_DIR=""
BACKUP_DIR="/tmp/casbin-mesh-backups"
MAX_BACKUP_AGE_DAYS=7
DRY_RUN=false
VERBOSE=false

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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
Usage: $0 [COMMAND] [OPTIONS]

Commands:
    cleanup     Clean up old data files and logs
    backup      Create backup of data directory
    restore     Restore from backup
    stats       Show data directory statistics
    gc          Force garbage collection on value logs

Options:
    -d, --data-dir DIR      Data directory path (required)
    -b, --backup-dir DIR    Backup directory (default: $BACKUP_DIR)
    -a, --max-age DAYS      Maximum age for backups in days (default: $MAX_BACKUP_AGE_DAYS)
    --dry-run               Show what would be done without actually doing it
    -v, --verbose           Enable verbose output
    -h, --help              Show this help message

Examples:
    # Clean up old files
    $0 cleanup -d /data/casbin-mesh

    # Create backup
    $0 backup -d /data/casbin-mesh -b /backups

    # Show statistics
    $0 stats -d /data/casbin-mesh

    # Force garbage collection
    $0 gc -d /data/casbin-mesh

EOF
}

check_dependencies() {
    local deps=("du" "find" "tar")
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            log "ERROR" "Required dependency '$dep' not found"
            exit 1
        fi
    done
}

validate_data_dir() {
    if [ -z "$DATA_DIR" ]; then
        log "ERROR" "Data directory not specified. Use -d option."
        exit 1
    fi

    if [ ! -d "$DATA_DIR" ]; then
        log "ERROR" "Data directory '$DATA_DIR' does not exist"
        exit 1
    fi

    # Check for Casbin-Mesh data files
    if [ ! -f "$DATA_DIR/raft-config.json" ] && [ ! -d "$DATA_DIR/default-raft.db" ] && [ ! -d "$DATA_DIR/default-state.db" ]; then
        log "WARN" "Directory '$DATA_DIR' doesn't appear to contain Casbin-Mesh data"
    fi
}

get_directory_size() {
    local dir=$1
    du -sh "$dir" 2>/dev/null | cut -f1 || echo "0B"
}

get_file_count() {
    local dir=$1
    find "$dir" -type f 2>/dev/null | wc -l
}

show_stats() {
    validate_data_dir
    
    log "INFO" "Analyzing data directory: $DATA_DIR"
    echo ""
    
    # Overall statistics
    local total_size=$(get_directory_size "$DATA_DIR")
    local total_files=$(get_file_count "$DATA_DIR")
    
    echo "📊 Overall Statistics"
    echo "  Total Size: $total_size"
    echo "  Total Files: $total_files"
    echo ""
    
    # Breakdown by component
    echo "📁 Component Breakdown"
    
    for subdir in "default-raft.db" "default-state.db" "snapshots"; do
        local path="$DATA_DIR/$subdir"
        if [ -d "$path" ]; then
            local size=$(get_directory_size "$path")
            local files=$(get_file_count "$path")
            echo "  $subdir: $size ($files files)"
        fi
    done
    echo ""
    
    # Value log analysis (Badger specific)
    echo "📄 Value Log Files (*.vlog)"
    if find "$DATA_DIR" -name "*.vlog" 2>/dev/null | grep -q .; then
        find "$DATA_DIR" -name "*.vlog" -exec ls -lh {} \; 2>/dev/null | \
            awk '{print "  " $9 ": " $5 " (modified: " $6 " " $7 " " $8 ")"}'
    else
        echo "  No value log files found"
    fi
    echo ""
    
    # Log files
    echo "📝 Log Files"
    if find "$DATA_DIR" -name "*.log" 2>/dev/null | grep -q .; then
        find "$DATA_DIR" -name "*.log" -exec ls -lh {} \; 2>/dev/null | \
            awk '{print "  " $9 ": " $5 " (modified: " $6 " " $7 " " $8 ")"}'
    else
        echo "  No log files found"
    fi
}

cleanup_old_files() {
    validate_data_dir
    
    log "INFO" "Starting cleanup of old files in $DATA_DIR"
    
    local cleaned=false
    
    # Clean up old snapshots (keep last 5)
    local snapshot_dir="$DATA_DIR/snapshots"
    if [ -d "$snapshot_dir" ]; then
        log "DEBUG" "Checking snapshots in $snapshot_dir"
        local snapshot_count=$(find "$snapshot_dir" -name "*.snap" 2>/dev/null | wc -l)
        if [ "$snapshot_count" -gt 5 ]; then
            log "INFO" "Found $snapshot_count snapshots, keeping latest 5"
            if [ "$DRY_RUN" = "true" ]; then
                log "INFO" "[DRY RUN] Would remove old snapshots"
            else
                find "$snapshot_dir" -name "*.snap" -type f -print0 | \
                    xargs -0 ls -t | tail -n +6 | xargs -r rm -f
                log "INFO" "Removed old snapshots"
                cleaned=true
            fi
        fi
    fi
    
    # Clean up old log files (older than 7 days)
    log "DEBUG" "Checking for old log files"
    local old_logs=$(find "$DATA_DIR" -name "*.log" -type f -mtime +7 2>/dev/null || true)
    if [ -n "$old_logs" ]; then
        log "INFO" "Found old log files (>7 days)"
        if [ "$DRY_RUN" = "true" ]; then
            echo "$old_logs" | while read -r logfile; do
                log "INFO" "[DRY RUN] Would remove: $logfile"
            done
        else
            echo "$old_logs" | xargs -r rm -f
            log "INFO" "Removed old log files"
            cleaned=true
        fi
    fi
    
    # Clean up temporary files
    log "DEBUG" "Checking for temporary files"
    local temp_files=$(find "$DATA_DIR" -name "*.tmp" -o -name "*.temp" -o -name "*.bak" 2>/dev/null || true)
    if [ -n "$temp_files" ]; then
        log "INFO" "Found temporary files"
        if [ "$DRY_RUN" = "true" ]; then
            echo "$temp_files" | while read -r tempfile; do
                log "INFO" "[DRY RUN] Would remove: $tempfile"
            done
        else
            echo "$temp_files" | xargs -r rm -f
            log "INFO" "Removed temporary files"
            cleaned=true
        fi
    fi
    
    if [ "$cleaned" = "false" ]; then
        log "INFO" "No cleanup needed"
    fi
}

create_backup() {
    validate_data_dir
    
    local timestamp=$(date '+%Y%m%d_%H%M%S')
    local backup_name="casbin-mesh-backup-$timestamp"
    local backup_path="$BACKUP_DIR/$backup_name.tar.gz"
    
    log "INFO" "Creating backup of $DATA_DIR"
    
    # Create backup directory if it doesn't exist
    if [ "$DRY_RUN" = "true" ]; then
        log "INFO" "[DRY RUN] Would create backup: $backup_path"
        return
    fi
    
    mkdir -p "$BACKUP_DIR"
    
    # Create compressed backup
    log "DEBUG" "Creating compressed archive: $backup_path"
    tar -czf "$backup_path" -C "$(dirname "$DATA_DIR")" "$(basename "$DATA_DIR")"
    
    if [ $? -eq 0 ]; then
        local backup_size=$(get_directory_size "$backup_path")
        log "INFO" "Backup created successfully: $backup_path ($backup_size)"
        
        # Clean up old backups
        cleanup_old_backups
    else
        log "ERROR" "Backup failed"
        exit 1
    fi
}

cleanup_old_backups() {
    if [ ! -d "$BACKUP_DIR" ]; then
        return
    fi
    
    log "DEBUG" "Cleaning up backups older than $MAX_BACKUP_AGE_DAYS days"
    
    local old_backups=$(find "$BACKUP_DIR" -name "casbin-mesh-backup-*.tar.gz" -mtime +$MAX_BACKUP_AGE_DAYS 2>/dev/null || true)
    if [ -n "$old_backups" ]; then
        log "INFO" "Removing old backups (>$MAX_BACKUP_AGE_DAYS days)"
        if [ "$DRY_RUN" = "true" ]; then
            echo "$old_backups" | while read -r backup; do
                log "INFO" "[DRY RUN] Would remove: $backup"
            done
        else
            echo "$old_backups" | xargs -r rm -f
            log "INFO" "Removed old backups"
        fi
    fi
}

restore_backup() {
    local backup_file="$1"
    
    if [ -z "$backup_file" ]; then
        log "ERROR" "Backup file not specified"
        echo "Available backups:"
        ls -la "$BACKUP_DIR"/casbin-mesh-backup-*.tar.gz 2>/dev/null || echo "No backups found"
        exit 1
    fi
    
    if [ ! -f "$backup_file" ]; then
        log "ERROR" "Backup file '$backup_file' not found"
        exit 1
    fi
    
    if [ -d "$DATA_DIR" ]; then
        log "WARN" "Data directory '$DATA_DIR' already exists"
        read -p "Do you want to overwrite it? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            log "INFO" "Restore cancelled"
            exit 0
        fi
    fi
    
    if [ "$DRY_RUN" = "true" ]; then
        log "INFO" "[DRY RUN] Would restore backup: $backup_file to $DATA_DIR"
        return
    fi
    
    log "INFO" "Restoring backup: $backup_file"
    
    # Remove existing data directory
    rm -rf "$DATA_DIR"
    
    # Extract backup
    tar -xzf "$backup_file" -C "$(dirname "$DATA_DIR")"
    
    if [ $? -eq 0 ]; then
        log "INFO" "Restore completed successfully"
    else
        log "ERROR" "Restore failed"
        exit 1
    fi
}

force_gc() {
    validate_data_dir
    
    log "INFO" "This will trigger garbage collection by creating a marker file"
    log "INFO" "Casbin-Mesh must be restarted to pick up the marker"
    
    if [ "$DRY_RUN" = "true" ]; then
        log "INFO" "[DRY RUN] Would create GC marker file in $DATA_DIR"
        return
    fi
    
    # Create a marker file that the application can detect
    local gc_marker="$DATA_DIR/force-gc.marker"
    echo "$(date)" > "$gc_marker"
    log "INFO" "Created GC marker file: $gc_marker"
    log "INFO" "Restart Casbin-Mesh to trigger garbage collection"
}

# Parse command line arguments
COMMAND=""
while [[ $# -gt 0 ]]; do
    case $1 in
        cleanup|backup|restore|stats|gc)
            COMMAND=$1
            shift
            ;;
        -d|--data-dir)
            DATA_DIR="$2"
            shift 2
            ;;
        -b|--backup-dir)
            BACKUP_DIR="$2"
            shift 2
            ;;
        -a|--max-age)
            MAX_BACKUP_AGE_DAYS="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
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
            if [ "$COMMAND" = "restore" ] && [ -z "$RESTORE_FILE" ]; then
                RESTORE_FILE="$1"
                shift
            else
                log "ERROR" "Unknown argument: $1"
                usage
                exit 1
            fi
            ;;
    esac
done

# Validate command
if [ -z "$COMMAND" ]; then
    log "ERROR" "No command specified"
    usage
    exit 1
fi

# Check dependencies
check_dependencies

# Execute command
case $COMMAND in
    stats)
        show_stats
        ;;
    cleanup)
        cleanup_old_files
        ;;
    backup)
        create_backup
        ;;
    restore)
        restore_backup "$RESTORE_FILE"
        ;;
    gc)
        force_gc
        ;;
    *)
        log "ERROR" "Unknown command: $COMMAND"
        usage
        exit 1
        ;;
esac