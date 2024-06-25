#!/bin/bash

make nfs
docker build -t nfsplugin:latest .
kind load docker-image nfsplugin:latest
deploy/msau-deploy.sh

