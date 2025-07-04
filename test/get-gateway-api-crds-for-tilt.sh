#!/bin/bash

curl -sL https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/experimental-install.yaml -o /tmp/experimental-install.yaml

echo "$(</tmp/experimental-install.yaml)"
