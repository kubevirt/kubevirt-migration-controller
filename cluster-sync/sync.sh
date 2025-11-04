#!/bin/bash

set -ex

source ./cluster-up/hack/common.sh
source ./cluster-up/cluster/${KUBEVIRT_PROVIDER}/provider.sh

make undeploy || echo "this is fine"

if [[ "$DOCKER_REPO" == "localhost" ]]; then
  port=$(./cluster-up/cli.sh ports registry | xargs)
  DOCKER_REPO_IMAGE="${DOCKER_REPO}:${port}/${IMG}"
  export DOCKER_PUSH_FLAGS="--tls-verify=false"
else
  DOCKER_REPO_IMAGE="${DOCKER_REPO}/${IMG}"
fi

# push to local registry provided by kvci
make docker-build DOCKER_REPO_IMAGE="${DOCKER_REPO_IMAGE}" 
make docker-push DOCKER_REPO_IMAGE="${DOCKER_REPO_IMAGE}"
# the "cluster" (kvci VM) only understands the alias registry:5000 (which maps to localhost:${port})
if [[ "$DOCKER_REPO" == "localhost" ]]; then
  MANIFEST_IMG="registry:5000/${IMG}"
else
  MANIFEST_IMG="${DOCKER_REPO}/${IMG}"
fi
make deploy MANIFEST_IMG="${MANIFEST_IMG}"
