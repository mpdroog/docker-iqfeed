#!/bin/bash
set -euo pipefail
IFS=$'\n\t'
IQFEED_INSTALLER_BIN="iqfeed_client_6_2_0_25.exe"

# Download IQFeed binary (so we only download it once)
# mkdir cache
# wget -nv http://www.iqfeed.net/$IQFEED_INSTALLER_BIN -O ./cache/$IQFEED_INSTALLER_BIN

# Build the API-tool
cd uptool
env GOOS=linux GOARCH=amd64 go build
cd -

# Build the container
docker build --tag 'docker-iqfeed' .
# Run it
docker run -p 9100:9101 -p 8080:8080 --cap-drop ALL --security-opt no-new-privileges --memory=256m --cpus=1 --rm --env-file iqfeed.env docker-iqfeed