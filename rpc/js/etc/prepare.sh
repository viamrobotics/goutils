#!/bin/bash

rm -rf src/gen

# Ours
mkdir -p src/gen/proto/rpc
cp -R ../../dist/js/proto/rpc/v1 src/gen/proto/rpc
cp -R ../../dist/js/proto/rpc/webrtc src/gen/proto/rpc

# Third-Party
mkdir -p src/gen/google
cp -R ../../dist/js/google/rpc src/gen/google
cp -R ../../dist/js/google/api src/gen/google

