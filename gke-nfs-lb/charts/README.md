# Install NFS CSI LB driver with Helm

## Prerequisites
 - [install Helm](https://helm.sh/docs/intro/quickstart/#install-helm)

### install a specific version
```console
$ cd <path/to/your/repo>/gke-nfs-lb/charts
$ helm upgrade -i nfs-csi-lb v0.0.1/nfs-csi-lb --set image.nfs.repository="gcr.io/gcs-tess/nfsplugin" --set image.nfs.tag="latest" --set controller.ipaddressList="10.4.0.45\,10.4.0.24\,10.4.0.42\,10.4.0.157\,10.4.0.22\,10.4.0.56\,10.4.0.143\,10.4.0.33\,10.4.0.162\,10.4.0.30\,10.4.0.169\,10.4.0.165\,10.4.0.43\,10.4.0.7\,10.4.0.154\,10.4.0.166\,10.4.0.13\,10.4.0.36\,10.4.0.6\,10.4.0.181\,10.4.0.164\,10.4.0.8\,10.4.0.50\,10.4.0.147\,10.4.0.174\,10.4.0.153\,10.4.0.35\,10.4.0.179\,10.4.0.18\,10.4.0.16\,10.4.0.155\,10.4.0.55\,10.4.0.152\,10.4.0.173\,10.4.0.62\,10.4.0.14\,10.4.0.177\,10.4.0.40\,10.4.0.167\,10.4.0.161\,10.4.0.160\,10.4.0.172\,10.4.0.32\,10.4.0.9\,10.4.0.57\,10.4.0.151\,10.4.0.148\,10.4.0.176\,10.4.0.158\,10.4.0.150\,10.4.0.5\,10.4.0.146\,10.4.0.58\,10.4.0.180\,10.4.0.170\,10.4.0.54\,10.4.0.159\,10.4.0.168\,10.4.0.38\,10.4.0.175\,10.4.0.149\,10.4.0.156\,10.4.0.26\,10.4.0.178"
```

### uninstall the driver
```console
$ helm uninstall nfs-csi-lb
```


TODO: Provide steps how to update the IP list