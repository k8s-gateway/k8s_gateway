#!/bin/bash

curl -sL https://kubernetes-sigs.github.io/external-dns/v0.16.1/docs/sources/crd/crd-manifest.yaml -o /tmp/dnsendpoint.yaml

echo "$(</tmp/dnsendpoint.yaml)"
