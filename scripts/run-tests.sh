#!/bin/bash
set -e

SERVER_URL="${1:-http://localhost:8080}"
echo "Running tests against: $SERVER_URL"

# Run functional tests
echo ""
echo "=== FUNCTIONAL TESTS ==="
cd test
go run functional_test.go
cd ..

# Run benchmark
echo ""
echo "=== BENCHMARK (5 seconds) ==="
cd test
SERVER_URL=$SERVER_URL NUM_CLIENTS=10 go run benchmark.go
cd ..

echo ""
echo "=== ALL TESTS COMPLETED ==="