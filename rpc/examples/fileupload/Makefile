ROOT_DIR:=$(shell dirname $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))/../../../..)
all: build
.PHONY: all

build:
	cd frontend && npm install && npx webpack

run_server: build
	go run server/cmd/main.go

setup_auth:
	cd ${ROOT_DIR} && make setup-cert setup-priv-key

run_server_auth: build setup_auth
	go run server/cmd/main.go -api_key=supersecretkeyohmy \
		-auth_private_key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls_cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls_key=${ROOT_DIR}/etc/test_keys/localhost/key.pem

clean:
	rm -rf frontend/dist
