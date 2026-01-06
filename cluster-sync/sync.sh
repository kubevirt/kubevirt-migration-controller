#!/bin/bash

set -ex

source ./cluster-up/hack/common.sh
source ./cluster-up/cluster/${KUBEVIRT_PROVIDER}/provider.sh

CERT_MANAGER_VERSION=${CERT_MANAGER_VERSION:-v1.16.3}

make undeploy || echo "this is fine"

if [[ "$DOCKER_REPO" == "localhost" ]]; then
  port=$(./cluster-up/cli.sh ports registry | xargs)
  DOCKER_REPO_IMAGE="${DOCKER_REPO}:${port}/${IMG}"
  export BUILDAH_TLS_VERIFY=false
else
  DOCKER_REPO_IMAGE="${DOCKER_REPO}/${IMG}"
fi

# push to local registry provided by kvci
make buildah-manifest-clean DOCKER_REPO_IMAGE="${DOCKER_REPO_IMAGE}" && make buildah-manifest DOCKER_REPO_IMAGE="${DOCKER_REPO_IMAGE}" && make buildah-manifest-push DOCKER_REPO_IMAGE="${DOCKER_REPO_IMAGE}"
# the "cluster" (kvci VM) only understands the alias registry:5000 (which maps to localhost:${port})
if [[ "$DOCKER_REPO" == "localhost" ]]; then
  MANIFEST_IMG="registry:5000/${IMG}"
else
  MANIFEST_IMG="${DOCKER_REPO}/${IMG}"
fi
make deploy MANIFEST_IMG="${MANIFEST_IMG}"

if [[ "$KUBEVIRT_PROVIDER" != "external" ]]; then
# Install CertManager
_kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml
_kubectl wait deployment.apps/cert-manager-webhook --for condition=Available --namespace cert-manager --timeout 5m
fi
