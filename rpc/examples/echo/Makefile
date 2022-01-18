ROOT_DIR:=$(shell dirname $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))/../../../..)
all: build
.PHONY: all

build:
	cd frontend && npm install && npm link @viamrobotics/rpc && npx webpack

run_server: build
	go run server/cmd/main.go

run_client:
	go run client/main.go -insecure

setup_auth:
	cd ${ROOT_DIR} && make setup-cert setup-priv-key

run_server_auth: build setup_auth
	go run server/cmd/main.go -api_key=supersecretkeyohmy \
		-auth_private_key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls_cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls_key=${ROOT_DIR}/etc/test_keys/localhost/key.pem

run_server_auth_internal: build setup_auth
	go run server/cmd/main.go -api_key=supersecretkeyohmy \
		-auth_public_key=${ROOT_DIR}/etc/test_keys/public-key.pem \
		-tls_cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls_key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external_auth_addr=https://localhost:8081

run_server_auth_external: build setup_auth
	go run server/cmd/main.go -api_key=supersecretkeyohmy \
		-auth_private_key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls_cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls_key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external_auth 8081

run_server_auth_internal_as_external: build setup_auth
	go run server/cmd/main.go -api_key=supersecretkeyohmy \
		-auth_public_key=${ROOT_DIR}/etc/test_keys/public-key.pem \
		-auth_private_key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls_cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls_key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external_auth_addr=https://localhost:8081 \
		-external_auth

run_client_auth: setup_auth
	go run client/main.go -api_key=supersecretkeyohmy

run_client_auth_external: setup_auth
	go run client/main.go -api_key=supersecretkeyohmy -external_auth_addr=localhost:8081

run_client_auth_external_direct: setup_auth
	go run client/main.go -api_key=supersecretkeyohmy -external_auth_addr=localhost:8081 -host=localhost:8080 -signaling_server=

clean:
	rm -rf frontend/dist
