#!/bin/bash

# Build script for health check tool
echo "Building Casbin-Mesh Health Check Tool..."

# Create build directory if it doesn't exist
mkdir -p build

# Build the health check tool
echo "Building health-check tool..."
go build -o build/health-check ./cmd/health-check/

if [ $? -eq 0 ]; then
    echo "✓ Health check tool built successfully: build/health-check"
    echo ""
    echo "Usage examples:"
    echo "  # Check single node health"
    echo "  ./build/health-check -node 127.0.0.1:8080"
    echo ""
    echo "  # Check cluster health"
    echo "  ./build/health-check -node 127.0.0.1:8080 -cluster"
    echo ""
    echo "  # Continuous monitoring"
    echo "  ./build/health-check -node 127.0.0.1:8080 -cluster -watch"
    echo ""
    echo "  # JSON output"
    echo "  ./build/health-check -node 127.0.0.1:8080 -cluster -format json"
else
    echo "✗ Build failed"
    exit 1
fi