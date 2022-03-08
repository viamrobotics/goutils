#!/bin/bash

mkdir -p test_keys
cd test_keys
if [[ -f "localhost/cert.pem" && -f "localhost/key.pem" ]]; then
	exit 0;
fi
go install github.com/jsha/minica@latest
minica --domains "localhost,local,*.place.local,echo-server,echo-server-external,fileupload-server,fileupload-server-external"
#TODO(GOUT-14): support linux
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain minica.pem 
