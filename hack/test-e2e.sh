#!/bin/bash

set -ex
export KUBEVIRT_PROVIDER=k8s-1.32
#Set the KubeVirt release to 1.6.1
KV_RELEASE=${KV_RELEASE:-v1.6.1} make cluster-up

make cluster-sync

kubectl=${PWD}/cluster-up/kubectl.sh

$kubectl get pods -n kubevirt

make test-e2e