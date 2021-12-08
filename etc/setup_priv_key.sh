#!/bin/bash

mkdir -p test_keys
cd test_keys
if [[ -f "private-key.pem" && -f "pkcs8.key" ]]; then
	exit 0;
fi
openssl genrsa -out private-key.pem 4096 
openssl pkcs8 -topk8 -inform PEM -outform PEM -nocrypt -in private-key.pem -out pkcs8.key
openssl rsa -in private-key.pem -outform PEM -pubout -out public-key.pem
