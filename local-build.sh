#!/usr/bin/env bash
echo "Starting plugin build"

echo "Docker cleanup"
docker rm $(docker ps -qa)
docker image prune -f
docker volume prune -f

echo "Disabling the plugin if it exists"
docker plugin disable docker-plugin-swo

echo "Removing the plugin if it exists"
docker plugin rm docker-plugin-swo

#######################
echo "Executable cleanup"
rm -f docker-swo-log-driver

echo "Building executable"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o output/docker-swo-log-driver
#######################

echo "cleanup"
rm -rf swo/

echo "Recreating directory structure"
mkdir -p swo/rootfs

echo "Copying configs"
cp config.json swo/

echo "Building docker image"
docker build -t rootfsimage -f output/Dockerfile.build output/

echo "Executable cleanup"
rm -f docker-swo-log-driver

echo "Creating a container with the image"
id=$(docker create rootfsimage true)

echo "Exporting the container fs"
docker export "$id" > rootfs.tar
docker rm -vf "$id"
docker rmi rootfsimage

echo "Extracting the tar'd root fs"
sudo tar -x --owner root --group root --no-same-owner -C swo/rootfs < rootfs.tar

echo "Removing the tar file"
rm -f rootfs.tar

echo "Setting the plugin up"
docker plugin create docker-plugin-swo swo/

echo "Enabling the plugin"
docker plugin enable docker-plugin-swo

echo "All done. Please proceed to use the log plugin."

# for logs: journalctl -u docker.service -f
# test container: docker run --rm --log-driver docker-plugin-swo --log-opt swo-url=https://your-swo-endpoint/logs --log-opt swo-token=YOUR_TOKEN ubuntu bash -c 'while true; do date +%s%N | sha256sum | base64 | head -c 32 ; echo " - Hello world"; sleep 10; done'
