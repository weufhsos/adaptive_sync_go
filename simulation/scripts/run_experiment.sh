#!/bin/bash

# Go-SDN AC Simulation - Experiment Runner
# Usage: ./run_experiment.sh <scenario>
#   Scenarios: baseline, burst, stress, comparison

set -e

cd "$(dirname "$0")/.."

SCENARIO=${1:-baseline}

echo "=========================================="
echo "  Running Experiment: $SCENARIO"
echo "=========================================="

case $SCENARIO in
    baseline)
        echo "Baseline test - Normal load"
        export RPS=5
        export DURATION=300s
        export PATTERN=uniform
        ;;
    burst)
        echo "Burst test - Sudden load spikes"
        export RPS=20
        export DURATION=600s
        export PATTERN=burst
        ;;
    stress)
        echo "Stress test - High load"
        export RPS=200
        export DURATION=300s
        export PATTERN=uniform
        ;;
    comparison)
        echo "Comparison test - For AC vs non-AC comparison"
        export RPS=100
        export DURATION=300s
        export PATTERN=uniform
        ;;
    *)
        echo "Unknown scenario: $SCENARIO"
        echo "Available: baseline, burst, stress, comparison"
        exit 1
        ;;
esac

echo ""
echo "Configuration:"
echo "  RPS: $RPS"
echo "  Duration: $DURATION"
echo "  Pattern: $PATTERN"
echo ""

# Start services if not running
if ! docker-compose ps | grep -q "sdn-1.*Up"; then
    echo "Starting SDN cluster..."
    ./scripts/start.sh
    sleep 10
fi

# Update loadgen environment and start
echo "Starting load generator with scenario: $SCENARIO"
docker-compose run --rm \
    -e RPS=$RPS \
    -e DURATION=$DURATION \
    -e PATTERN=$PATTERN \
    -e TARGET_NODES=sdn-1:8080,sdn-2:8080,sdn-3:8080,sdn-4:8080,sdn-5:8080 \
    loadgen

echo ""
echo "=========================================="
echo "  Experiment Completed: $SCENARIO"
echo "=========================================="
echo ""
echo "View results in Grafana: http://localhost:3000"
