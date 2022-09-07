# Example gRPC File Upload Server

This example server demonstrates how to run a file upload server via gRPC accessible via `grpc`, `grpc-web`, `grpc-gateway`, and `grpc-over-webrtc` all on the same port while hosting other HTTP services. This is a browser only example but a go client could still be created like the echo example has.

Note: For the web, `grpc-web` (Direct) will not work until https://github.com/grpc/grpc-web/issues/24 is done; WebRTC will work however.

## Build

`make build`

## Run

1. `make run-server`

### With auth

1. `make run-server-auth`

### With an external auth source

1. `make run-server-auth-internal` # Use the UI on this one
1. `make run-server-auth-external`

## Using

1. Go to [http://localhost:8080](http://localhost:8080), upload a file, and check the terminal output.
