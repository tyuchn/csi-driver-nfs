#!/bin/bash

make nfs
docker build -t nfsplugin:latest .
kind load docker-image nfsplugin:latest
make build-lb-controller-image-and-push
deploy/msau-deploy.sh

