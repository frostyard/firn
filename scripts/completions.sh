#!/bin/sh
set -e
rm -rf completions
mkdir completions
go build -o build/firn ./cmd/firn-cli
for sh in bash zsh fish; do
  ./build/firn completion "$sh" >"completions/firn.$sh"
done
