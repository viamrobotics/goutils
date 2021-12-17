# Example gRPC Echo Server

This example server demonstrates how to run gRPC accessible via `grpc`, `grpc-web`, and `grpc-gateway` all on the same port while hosting other HTTP services.

## Build

`make build`

## Run

`make run_server`
`make run_client`

### With auth

`make run_server_auth`
`make run_client_auth`

### With an external auth source

`make run_server_auth_internal` # Use the UI on this one
`make run_server_auth_external`
`make run_client_auth_external`

## Using

1. Go to [http://localhost:8080](http://localhost:8080) and look at the developer console.
1. Try `curl -XPOST http://localhost:8080/api/v1/echo\?message\=foo`
1. Try `grpcurl -plaintext -d='{"message": "hey"}' localhost:8080 proto.rpc.examples.echo.v1.EchoService/Echo`
