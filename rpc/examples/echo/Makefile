ROOT_DIR:=$(shell dirname $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))/../../../..)

run-server:
	go run server/cmd/main.go -instance-name=echo-server

run-client:
	go run client/main.go -host=echo-server

setup-auth:
	cd ${ROOT_DIR} && make setup-cert setup-priv-key

run-server-auth: setup-auth
	go run server/cmd/main.go -instance-name=echo-server -api-key=supersecretkeyohmy \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem

run-server-auth-tls: setup-auth
	go run server/cmd/main.go -instance-name=echo-server \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-tls-auth

run-server-auth-internal: setup-auth
	go run server/cmd/main.go -instance-name=echo-server -api-key=supersecretkeyohmy \
		-auth-public-key=${ROOT_DIR}/etc/test_keys/public-key.pem \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external-auth-addr=https://localhost:8081

run-server-auth-external: setup-auth
	go run server/cmd/main.go -instance-name=echo-server-external -api-key=supersecretkeyohmy \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external-auth 8081

run-server-auth-with-access-token: setup-auth
	go run server/cmd/main.go -instance-name=echo-server-external -api-key=supersecretkeyohmy \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-use-access-token

run-server-auth-with-external-access-token: setup-auth
	go run server/cmd/main.go -instance-name=echo-server-external -api-key=supersecretkeyohmy \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-auth-public-key=${ROOT_DIR}/etc/test_keys/public-key.pem \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-use-access-token \
		-external-auth-addr=https://localhost:8081 \
		-external-auth 8081

run-server-auth-internal-as-external: setup-auth
	go run server/cmd/main.go -instance-name=echo-server -api-key=supersecretkeyohmy \
		-auth-public-key=${ROOT_DIR}/etc/test_keys/public-key.pem \
		-auth-private-key=${ROOT_DIR}/etc/test_keys/pkcs8.key \
		-tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem \
		-external-auth-addr=https://localhost:8081 \
		-external-auth

run-client-auth: setup-auth
	go run client/main.go -host=echo-server -api-key=supersecretkeyohmy

run-client-auth-tls: setup-auth
	go run client/main.go -host=echo-server -tls-cert=${ROOT_DIR}/etc/test_keys/localhost/cert.pem \
		-tls-key=${ROOT_DIR}/etc/test_keys/localhost/key.pem

run-client-auth-external: setup-auth
	go run client/main.go -host=echo-server -api-key=supersecretkeyohmy -external-auth-addr=localhost:8081

run-client-auth-external-direct: setup-auth
	go run client/main.go -api-key=supersecretkeyohmy -external-auth-addr=localhost:8081 -host=localhost:8080
