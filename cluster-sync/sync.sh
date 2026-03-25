#!/bin/bash

set -ex

source ./cluster-up/hack/common.sh
source ./cluster-up/cluster/${KUBEVIRT_PROVIDER}/provider.sh

CERT_MANAGER_VERSION=${CERT_MANAGER_VERSION:-v1.16.3}

make undeploy || echo "this is fine"

if [[ "$DOCKER_REPO" == "localhost" ]]; then
  port=$(./cluster-up/cli.sh ports registry | xargs)
  DOCKER_REMOTE_REPO="${DOCKER_REPO}:${port}"
  export BUILDAH_TLS_VERIFY=false
else
  DOCKER_REMOTE_REPO="${DOCKER_REPO}"
fi

# push to local registry provided by kvci
make buildah-manifest-clean DOCKER_REPO="${DOCKER_REMOTE_REPO}" && make buildah-manifest DOCKER_REPO="${DOCKER_REMOTE_REPO}" && make buildah-manifest-push DOCKER_REPO="${DOCKER_REMOTE_REPO}"
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

function start_nginx_proxy() {
  # check if nginx proxy is already installed
  if _kubectl get namespace nginx-proxy &> /dev/null; then
    echo "Nginx proxy is already installed"
    return
  fi
  _kubectl create namespace nginx-proxy
  _kubectl apply -f nginx-proxy/nginx-ca.yaml -n nginx-proxy
  _kubectl apply -f nginx-proxy/nginx-cm.yaml -n nginx-proxy
  _kubectl apply -f nginx-proxy/nginx-secret.yaml -n nginx-proxy
  _kubectl apply -f nginx-proxy/nginx-svc.yaml -n nginx-proxy
  _kubectl apply -f nginx-proxy/nginx-deployment.yaml -n nginx-proxy
  _kubectl rollout status -n nginx-proxy deployment/nginx-registry-proxy --timeout=120s
}

start_nginx_proxy