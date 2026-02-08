#!/bin/bash

# Go-SDN AC Simulation - Start Script
# Usage: ./start.sh [options]
#   --with-loadgen    Include load generator
#   --build           Force rebuild images

set -e

cd "$(dirname "$0")/.."

echo "=========================================="
echo "  Go-SDN AC Simulation Environment"
echo "=========================================="

BUILD_FLAG=""
PROFILE_FLAG=""

# Parse arguments
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --with-loadgen) PROFILE_FLAG="--profile loadtest" ;;
        --build) BUILD_FLAG="--build" ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
    shift
done

echo "[1/3] Starting infrastructure services..."
docker-compose up -d prometheus grafana influxdb

echo "[2/3] Waiting for infrastructure to be ready..."
sleep 5

echo "[3/3] Preparing build context and starting SDN nodes..."
./prepare-build.sh
docker-compose up -d $BUILD_FLAG sdn-1 sdn-2 sdn-3 sdn-4 sdn-5

echo ""
echo "=========================================="
echo "  Services Started Successfully!"
echo "=========================================="
echo ""
echo "Access points:"
echo "  - SDN-1 API:    http://localhost:8081"
echo "  - SDN-2 API:    http://localhost:8082"
echo "  - SDN-3 API:    http://localhost:8083"
echo "  - SDN-4 API:    http://localhost:8084"
echo "  - SDN-5 API:    http://localhost:8085"
echo "  - Prometheus:   http://localhost:9090"
echo "  - Grafana:      http://localhost:3000 (admin/admin)"
echo "  - InfluxDB:     http://localhost:8086"
echo ""

if [[ -n "$PROFILE_FLAG" ]]; then
    echo "[+] Starting load generator..."
    docker-compose $PROFILE_FLAG up -d loadgen
    echo "  - Load generator running"
fi

echo ""
echo "Use './scripts/stop.sh' to stop all services"
echo "Use 'docker-compose logs -f sdn-1' to view logs"
