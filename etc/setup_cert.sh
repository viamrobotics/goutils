#!/bin/bash

mkdir -p $HOME/.minica
cd $HOME/.minica
go install github.com/jsha/minica@latest
minica --domains localhost
#TODO(erd): support linux
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain minica.pem 
