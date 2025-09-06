#!/bin/bash
set -e

# Configuration
SERVICE_NAME="websocket-relay"
REPO_URL="https://github.com/miguelemosreverte/websocket-relay.git"
DEPLOY_DIR="/root/websocket-relay"
BINARY_NAME="websocket-relay"
LOG_FILE="/root/${SERVICE_NAME}.log"
PID_FILE="/root/${SERVICE_NAME}.pid"
PORT=8080

echo "=== Deployment started at $(date) ==="

# Clone or pull repository
if [ ! -d "$DEPLOY_DIR" ]; then
    echo "Cloning repository..."
    git clone "$REPO_URL" "$DEPLOY_DIR"
    cd "$DEPLOY_DIR"
else
    echo "Updating repository..."
    cd "$DEPLOY_DIR"
    git fetch origin
    git reset --hard origin/main
    git pull origin main
fi

# Get commit information
COMMIT_HASH=$(git rev-parse --short HEAD)
BUILD_TIME=$(date -u +"%Y-%m-%d %H:%M:%S UTC")

echo "Building version: $COMMIT_HASH"

# Build the Go binary
echo "Building Go binary..."
COMMIT_HASH="$COMMIT_HASH" BUILD_TIME="$BUILD_TIME" go build -ldflags="-s -w" -o "$BINARY_NAME" main.go

# Stop existing service if running
if [ -f "$PID_FILE" ]; then
    OLD_PID=$(cat "$PID_FILE")
    if kill -0 "$OLD_PID" 2>/dev/null; then
        echo "Stopping existing service (PID: $OLD_PID)..."
        kill "$OLD_PID"
        sleep 2
        # Force kill if still running
        kill -9 "$OLD_PID" 2>/dev/null || true
    fi
    rm -f "$PID_FILE"
fi

# Start the new service
echo "Starting new service..."
export PORT=$PORT
export COMMIT_HASH="$COMMIT_HASH"
export BUILD_TIME="$BUILD_TIME"
nohup ./"$BINARY_NAME" > "$LOG_FILE" 2>&1 &
NEW_PID=$!
echo $NEW_PID > "$PID_FILE"

echo "Service started with PID: $NEW_PID"

# Wait for service to be ready
echo "Waiting for service to be ready..."
sleep 3

# Health check
echo "Performing health check..."
HEALTH_CHECK=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:$PORT/health || echo "000")

if [ "$HEALTH_CHECK" = "200" ]; then
    echo "✅ Health check passed!"
    echo "Service is running on port $PORT"
    curl -s http://localhost:$PORT/health | jq '.' || curl -s http://localhost:$PORT/health
    echo "=== Deployment completed successfully at $(date) ==="
    exit 0
else
    echo "❌ Health check failed! HTTP status: $HEALTH_CHECK"
    echo "Last 20 lines of log:"
    tail -20 "$LOG_FILE"
    exit 1
fi