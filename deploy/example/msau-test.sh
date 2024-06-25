#!/bin/bash

kubectl delete -f nginx-pod-nfs-static.yaml

kubectl apply -f pvc-nfs-csi-static.yaml
kubectl apply -f pv-nfs-csi.yaml
kubectl apply -f nginx-pod-nfs-static.yaml
