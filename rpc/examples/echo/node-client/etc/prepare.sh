#!/bin/bash

rm -rf src/gen

# Ours
mkdir -p src/gen/proto/rpc/examples
cp -R ../../../../dist/js/proto/rpc/examples/echo src/gen/proto/rpc/examples

# Third-Party
mkdir -p src/gen/google
cp -R ../../../../dist/js/google/api src/gen/google
