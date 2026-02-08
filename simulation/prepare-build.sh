#!/bin/bash
# Prepare build context for Docker containers

set -e

echo "Preparing build context..."

# Create build context directory
BUILD_CONTEXT="./build-context"
mkdir -p "$BUILD_CONTEXT"

# Copy main module files
cp ../go.mod ../go.sum "$BUILD_CONTEXT/"
cp ../*.go "$BUILD_CONTEXT/"

# Copy directories
cp -r ../dispatcher "$BUILD_CONTEXT/"
cp -r ../oca "$BUILD_CONTEXT/"
cp -r ../pi "$BUILD_CONTEXT/"
cp -r ../proto "$BUILD_CONTEXT/"
cp -r ../store "$BUILD_CONTEXT/"
cp -r ../transport "$BUILD_CONTEXT/"

# Copy simulation directories
cp -r ./mock-sdn "$BUILD_CONTEXT/"
cp -r ./loadgen "$BUILD_CONTEXT/"

echo "Build context prepared in $BUILD_CONTEXT"