#!/bin/bash

export IMAGE_VERSION=latest
make build-nfs-csi-image-and-push
make build-lb-controller-image-and-push
deploy/msau-deploy.sh

