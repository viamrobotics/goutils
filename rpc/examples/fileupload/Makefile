ROOT_DIR:=$(shell dirname $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))/../../../..)
all: build
.PHONY: all

build:
	cd frontend && npm install && npm link @viamrobotics/rpc && npx webpack

run-server: build
	go run server/cmd/main.go

setup-auth:
	cd ${ROOT_DIR} && make setup-cert setup-priv-key

run-server-auth: build setup-auth
	go run server/cmd/main.go -api-key=supersecretkeyohmy \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem

run-server-auth-internal: build setup-auth
	go run server/cmd/main.go -instance-name=fileupload-server -api-key=supersecretkeyohmy \
		-auth-public-key=${ROOT_DIR}/etc/test_keys/public-key.pem \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external-auth-addr=https://localhost:8081

run-server-auth-external: build setup-auth
	go run server/cmd/main.go -instance-name=fileupload-server-external -api-key=supersecretkeyohmy \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external-auth 8081

run-server-auth-internal-as-external: build setup-auth
	go run server/cmd/main.go -api-key=supersecretkeyohmy \
		-auth-public-key=${ROOT_DIR}/etc/test_keys/public-key.pem \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external-auth-addr=https://localhost:8081 \
		-external-auth

clean:
	rm -rf frontend/dist
