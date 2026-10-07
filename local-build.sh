#!/usr/bin/env bash
set -euo pipefail
test "$(id -u)" -eq 0 || { echo "error: run as root"; exit 1; }
echo "Starting plugin build"

echo "Running tests"
go test ./...
go test -race ./...

if docker plugin inspect docker-plugin-swo > /dev/null 2>&1; then
    if [ "$(docker plugin inspect --format '{{.Enabled}}' docker-plugin-swo)" = "true" ]; then
        echo "Disabling the plugin"
        docker plugin disable docker-plugin-swo
    fi
    echo "Removing the plugin"
    docker plugin rm docker-plugin-swo
fi

#######################
echo "Executable cleanup"
rm -rf output/

echo "Building executable"
mkdir -p output
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o output/docker-swo-log-driver ./cmd/docker-swo-log-driver
#######################

echo "cleanup"
rm -rf swo/

echo "Recreating directory structure"
mkdir -p swo/rootfs

echo "Copying configs"
cp config.json swo/

echo "Building docker image"
docker build -t rootfsimage -f Dockerfile .

echo "Creating a container with the image"
id=$(docker create rootfsimage true)

echo "Exporting the container fs"
docker export "$id" > rootfs.tar
docker rm -vf "$id"
docker rmi rootfsimage

echo "Extracting the tar'd root fs"
tar -x --owner root --group root --no-same-owner -C swo/rootfs < rootfs.tar

echo "Removing the tar file"
rm -f rootfs.tar

echo "Setting the plugin up"
docker plugin create docker-plugin-swo swo/

echo "Enabling the plugin"
docker plugin enable docker-plugin-swo

echo "All done. Please proceed to use the log plugin."

# for logs: journalctl -u docker.service -f
# test container: docker run --rm --log-driver docker-plugin-swo --log-opt swo-url=https://your-swo-endpoint/logs --log-opt swo-token=YOUR_TOKEN ubuntu bash -c 'while true; do date +%s%N | sha256sum | base64 | head -c 32 ; echo " - Hello world"; sleep 10; done'
