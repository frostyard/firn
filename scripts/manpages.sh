#!/bin/sh
set -e
rm -rf manpages
mkdir manpages
go run ./cmd/firn-cli man | gzip -c -9 >manpages/firn.1.gz
