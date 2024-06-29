#!/bin/bash

# Convert list of server IPs to the CSV format required by lb-controller.

cat ips.txt |sort| tr '\n' ',' > ips.csv
