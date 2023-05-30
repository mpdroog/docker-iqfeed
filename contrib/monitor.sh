#!/bin/bash
# Check if container is healthy and running
# - If no longer health (test.php returns failure then stop container)
# - If no longer running (spawn new instance with docker run detach)
set -euo pipefail
IFS=$'\n\t'

set +e
php test.php
exit_status=$?
set -e
if [ $exit_status -eq 1 ]; then
    echo "destroy iqfeed-container"
    ID=$(docker container ls -q --filter name=iqfeed)
    if [[ $ID ]]; then
        docker rm -f $ID > /dev/null
    fi
fi

# Spawn a docker instance if none running.
if [ -z $(docker container ls -q --filter name=iqfeed) ]; then
    echo "start new iqfeed-container"
    docker run -d -p 9100:9101 -p 8080:8080 --cap-drop ALL --security-opt no-new-privileges --memory=256m --cpus=1 --rm --name iqfeed --env-file /root/iqfeed.env mpdroog/docker-iqfeed > /dev/null
fi
