# Example gRPC Echo Server

This example server demonstrates how to run gRPC accessible via `grpc`, `grpc-web`, `grpc-gateway`, and `grpc-over-webrtc` all on the same port while hosting other HTTP services.

## Build

`make build`

## Run

1. `make run-server`
1. `make run-client`

### With auth

1. `make run-server-auth`
1. `make run-client-auth`

### With auth via mTLS (no UI)

1. `make run-server-auth-tls`
1. `make run-client-auth-tls`

### With an external auth source

1. `make run-server-auth-internal` # Use the UI on this one
1. `make run-server-auth-external`
1. `make run-client-auth-external`
