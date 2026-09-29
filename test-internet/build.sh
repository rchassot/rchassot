#!/bin/sh
# Compile TestInternet.exe pour Windows (fonctionne depuis Linux, macOS ou Windows).
set -e
cd "$(dirname "$0")"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o TestInternet.exe .
echo "OK : $(pwd)/TestInternet.exe"
