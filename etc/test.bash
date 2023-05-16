#!/bin/bash

DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
ROOT_DIR="$DIR/../"
cd $ROOT_DIR

if [[ "$1" == "cover" ]]; then
	COVER=-coverprofile=coverage.txt
fi

# race isn't supported on the pi or jetson (and possibly other arm boards)
# https://github.com/golang/go/issues/29948
if [ "$(uname -m)" != "aarch64" ] || [ "$(uname)" != "Linux" ]; then
	RACE=-race
fi

gotestsum --format standard-verbose --jsonfile json.log -- -tags=no_skip $RACE $COVER ./...
PID=$!

trap "kill -9 $PID" INT

FAIL=0
wait $PID || let "FAIL+=1"

cat json.log | go run ./etc/analyzetests/main.go

if [ "$FAIL" != "0" ]; then
	exit $FAIL
fi

cat coverage.txt | go run ./etc/analyzecoverage/main.go

if [[ "$1" == "cover" ]]; then
	gocov convert coverage.txt | gocov-xml > coverage.xml
fi
