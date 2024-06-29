#!/bin/bash

# Delete all the nfs.csi.k8s.io/assigned-ip- node annotations from all nodes
# Do this when we need to reset the IP distribution


nodes=( $(kubectl get node -o jsonpath='{.items[*].metadata.name}') )

for n in "${nodes[@]}"; do
  kubectl annotate node $n nfs.csi.k8s.io/assigned-ip-
done

