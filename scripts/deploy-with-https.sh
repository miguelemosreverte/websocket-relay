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
DOMAIN="${DOMAIN:-}"  # Domain will be passed as environment variable
SERVER_IP="95.217.238.72"

echo "=== HTTPS Deployment started at $(date) ==="

# Install Caddy if not installed
if ! command -v caddy &> /dev/null; then
    echo "Installing Caddy..."
    apt-get update
    apt-get install -y debian-keyring debian-archive-keyring apt-transport-https
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
    apt-get update
    apt-get install -y caddy
fi

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
COMMIT_HASH="$COMMIT_HASH" BUILD_TIME="$BUILD_TIME" go build -ldflags="-s -w" -o "$BINARY_NAME" main.go websocket.go

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

# Also check if any process is using port 8080 and kill it
echo "Checking for processes using port $PORT..."
PORT_PID=$(lsof -ti:$PORT 2>/dev/null || true)
if [ ! -z "$PORT_PID" ]; then
    echo "Found process $PORT_PID using port $PORT, stopping it..."
    kill -9 $PORT_PID 2>/dev/null || true
    sleep 2
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

# Configure Caddy
echo "Configuring Caddy..."
if [ -n "$DOMAIN" ]; then
    echo "Configuring Caddy with domain: $DOMAIN"
    cat > /etc/caddy/Caddyfile << EOF
# Serve with Let's Encrypt certificate for domain
${DOMAIN} {
    reverse_proxy localhost:${PORT}
    
    # WebSocket support
    @websocket {
        header Connection *Upgrade*
        header Upgrade websocket
    }
    reverse_proxy @websocket localhost:${PORT}
    
    # Enable compression
    encode gzip
    
    # Security headers
    header {
        X-Content-Type-Options nosniff
        X-Frame-Options DENY
        X-XSS-Protection "1; mode=block"
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        -Server
    }
    
    # Logging
    log {
        output file /var/log/caddy/access.log
        format json
    }
}

# Redirect www to non-www if applicable
www.${DOMAIN} {
    redir https://${DOMAIN}{uri} permanent
}

# Also serve on IP with self-signed cert for direct access
${SERVER_IP} {
    tls internal
    reverse_proxy localhost:${PORT}
    
    @websocket {
        header Connection *Upgrade*
        header Upgrade websocket
    }
    reverse_proxy @websocket localhost:${PORT}
}
EOF
else
    echo "No domain configured, using IP only with self-signed cert"
    cat > /etc/caddy/Caddyfile << EOF
# Serve on IP with self-signed cert
${SERVER_IP} {
    tls internal
    reverse_proxy localhost:${PORT}
    
    @websocket {
        header Connection *Upgrade*
        header Upgrade websocket
    }
    reverse_proxy @websocket localhost:${PORT}
    
    # Enable compression
    encode gzip
    
    # Security headers
    header {
        X-Content-Type-Options nosniff
        X-Frame-Options DENY
        X-XSS-Protection "1; mode=block"
        -Server
    }
}

# Redirect HTTP to HTTPS
:80 {
    redir https://{host}{uri} permanent
}
EOF
fi

# Create log directory if it doesn't exist
mkdir -p /var/log/caddy

# Reload Caddy
echo "Reloading Caddy..."
systemctl reload caddy || systemctl restart caddy

# Wait for services to be ready
echo "Waiting for services to be ready..."
sleep 5

# Health check on localhost first
echo "Performing health check on localhost..."
HEALTH_CHECK=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:$PORT/health || echo "000")

if [ "$HEALTH_CHECK" = "200" ]; then
    echo "✅ Local health check passed!"
    curl -s http://localhost:$PORT/health | jq '.' || curl -s http://localhost:$PORT/health
else
    echo "❌ Local health check failed! HTTP status: $HEALTH_CHECK"
    echo "Last 20 lines of log:"
    tail -20 "$LOG_FILE"
    exit 1
fi

# Test HTTPS endpoint
echo "Testing HTTPS endpoint..."
if [ -n "$DOMAIN" ]; then
    # Test domain with Let's Encrypt cert
    echo "Testing https://${DOMAIN}/health"
    HTTPS_CHECK=$(curl -s -o /dev/null -w "%{http_code}" https://${DOMAIN}/health || echo "000")
    
    if [ "$HTTPS_CHECK" = "200" ]; then
        echo "✅ HTTPS health check passed with Let's Encrypt!"
        echo "Service is available at:"
        echo "  - https://${DOMAIN}/ (with Let's Encrypt cert)"
        echo "  - https://${SERVER_IP}/ (with self-signed cert)"
        curl -s https://${DOMAIN}/health | jq '.' || curl -s https://${DOMAIN}/health
        echo "=== HTTPS Deployment completed successfully at $(date) ==="
        exit 0
    else
        echo "⚠️  Domain HTTPS check returned: $HTTPS_CHECK"
        echo "Let's Encrypt may still be provisioning the certificate..."
    fi
fi

# Test IP with self-signed cert
HTTPS_CHECK=$(curl -k -s -o /dev/null -w "%{http_code}" https://${SERVER_IP}/health || echo "000")

if [ "$HTTPS_CHECK" = "200" ]; then
    echo "✅ HTTPS health check passed on IP!"
    echo "Service is available at:"
    if [ -n "$DOMAIN" ]; then
        echo "  - https://${DOMAIN}/ (Let's Encrypt cert may be provisioning)"
    fi
    echo "  - https://${SERVER_IP}/ (with self-signed cert)"
    echo "=== HTTPS Deployment completed successfully at $(date) ==="
    exit 0
else
    echo "⚠️  HTTPS health check returned: $HTTPS_CHECK"
    echo "Checking Caddy status..."
    systemctl status caddy --no-pager || true
    echo "=== Deployment completed with warnings at $(date) ==="
    exit 0
fi