# Example gRPC Echo Server

This example server demonstrates how to run gRPC accessible via `grpc`, `grpc-web`, and `grpc-gateway` all on the same port while hosting other HTTP services.

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

## Using

1. Go to [http://localhost:8080](http://localhost:8080) and look at the developer console.
1. Try `curl -XPOST http://localhost:8080/api/v1/echo\?message\=foo`
1. Try `grpcurl -plaintext -d='{"message": "hey"}' localhost:8080 proto.rpc.examples.echo.v1.EchoService/Echo`
