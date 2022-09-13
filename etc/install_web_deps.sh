#!/bin/bash

if [ ! -d "rpc/js/node_modules" ]; then
  npm i --ignore-scripts --prefix ../rpc/js
else
  npm ci --ignore-scripts --prefix ../rpc/js
fi