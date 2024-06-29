#!/bin/bash

# Copyright 2020 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

repo="./deploy"

echo "Deleting NFS CSI driver"
kubectl delete -f $repo/csi-nfs-node-lb.yaml --ignore-not-found
kubectl delete -f $repo/lb-controller.yaml --ignore-not-found
kubectl delete -f $repo/rbac-csi-nfs-node-lb.yaml --ignore-not-found
kubectl delete -f $repo/csi-nfs-lb-driverinfo.yaml --ignore-not-found
kubectl delete -f $repo/rbac-lb-controller.yaml --ignore-not-found
kubectl delete -f $repo/rbac-csi-nfs-lb.yaml --ignore-not-found
kubectl delete -f $repo/csi-nfs-controller-lb.yaml --ignore-not-found

echo "Installing NFS CSI driver"
kubectl apply -f $repo/rbac-csi-nfs-lb.yaml
kubectl apply -f $repo/csi-nfs-lb-driverinfo.yaml
kubectl apply -f $repo/csi-nfs-controller-lb.yaml
kubectl apply -f $repo/csi-nfs-node-lb.yaml

echo 'NFS CSI driver installed successfully.'
