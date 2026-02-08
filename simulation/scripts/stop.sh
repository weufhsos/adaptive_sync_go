#!/bin/bash

# Go-SDN AC Simulation - Stop Script

set -e

cd "$(dirname "$0")/.."

echo "Stopping all simulation services..."

docker-compose --profile loadtest down

# Clean up build context
if [ -d "build-context" ]; then
  rm -rf build-context
  echo "Build context cleaned up."
fi

echo "All services stopped."
