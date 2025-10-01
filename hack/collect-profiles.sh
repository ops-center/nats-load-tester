#!/bin/bash
#
# Collect pprof profiles from NATS Load Tester
#
# Usage:
#   ./collect-profiles.sh [duration] [output_dir] [port]
#
# Examples:
#   # Local binary
#   ./load-tester --enable-pprof &
#   ./collect-profiles.sh 60 ./profiles 9481
#
#   # Kubernetes (requires port-forward in another terminal)
#   kubectl port-forward deployment/nats-load-tester 9481:9481
#   ./collect-profiles.sh 60 ./profiles 9481

set -e

DURATION=${1:-60}
OUTPUT_DIR=${2:-./profiles}
PPROF_PORT=${3:-9481}

mkdir -p "$OUTPUT_DIR"

echo "Collecting profiles for ${DURATION}s to $OUTPUT_DIR"
echo "pprof endpoint: http://localhost:${PPROF_PORT}/debug/pprof/"
echo ""

echo "[1/6] Starting CPU profile (${DURATION}s)..."
curl -s "http://localhost:${PPROF_PORT}/debug/pprof/profile?seconds=$DURATION" > "$OUTPUT_DIR/cpu.prof" &
CPU_PID=$!

echo "[2/6] Collecting periodic heap snapshots..."
for i in {1..4}; do
    sleep $((DURATION / 4))
    echo "  - Heap snapshot $i/4"
    curl -s "http://localhost:${PPROF_PORT}/debug/pprof/heap" > "$OUTPUT_DIR/heap-${i}.prof"
done

echo "[3/6] Waiting for CPU profile to complete..."
wait $CPU_PID

echo "[4/6] Collecting goroutine profile..."
curl -s "http://localhost:${PPROF_PORT}/debug/pprof/goroutine" > "$OUTPUT_DIR/goroutine.prof"

echo "[5/6] Collecting block profile..."
curl -s "http://localhost:${PPROF_PORT}/debug/pprof/block" > "$OUTPUT_DIR/block.prof"

echo "[6/6] Collecting mutex profile..."
curl -s "http://localhost:${PPROF_PORT}/debug/pprof/mutex" > "$OUTPUT_DIR/mutex.prof"

echo ""
echo "Profile collection complete! Files saved to: $OUTPUT_DIR"
echo ""
echo "Analysis commands:"
echo "  CPU:        go tool pprof -http=:9090 $OUTPUT_DIR/cpu.prof"
echo "  Heap:       go tool pprof -http=:9090 $OUTPUT_DIR/heap-4.prof"
echo "  Goroutines: go tool pprof -http=:9090 $OUTPUT_DIR/goroutine.prof"
echo "  Block:      go tool pprof -http=:9090 $OUTPUT_DIR/block.prof"
echo "  Mutex:      go tool pprof -http=:9090 $OUTPUT_DIR/mutex.prof"
echo ""
echo "Quick goroutine count:"
go tool pprof -top $OUTPUT_DIR/goroutine.prof | head -1
