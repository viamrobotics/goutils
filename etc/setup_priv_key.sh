#!/bin/bash

mkdir -p test_keys
cd test_keys
if [[ -f "private-key.pem" && -f "pkcs8.key" ]]; then
	exit 0;
fi
openssl genpkey -algorithm ed25519 -out private-key.pem
openssl pkcs8 -topk8 -inform PEM -outform PEM -nocrypt -in private-key.pem -out pkcs8.key
openssl pkey -in private-key.pem -pubout -out public-key.pem
