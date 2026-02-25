#!/bin/bash
# Copyright 2025 The KubeVirt Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -ex
export KUBEVIRT_PROVIDER=k8s-1.32
#Set the KubeVirt release to 1.6.1
KV_RELEASE=${KV_RELEASE:-v1.6.1} make cluster-up

make cluster-sync

# Push alpine-with-test-tooling container disk into the kubevirtci cluster registry
# so VMs can use registry:5000/alpine-with-test-tooling-container-disk:latest
port=$(./cluster-up/cli.sh ports registry | xargs)
export BUILDAH_TLS_VERIFY=false
if podman pull --tls-verify=false "localhost:${port}/alpine-with-test-tooling-container-disk:latest" 2>/dev/null; then
  echo "alpine-with-test-tooling-container-disk:latest already in registry, skipping"
else
  podman pull quay.io/kubevirt/alpine-with-test-tooling-container-disk:v1.7.0
  podman tag quay.io/kubevirt/alpine-with-test-tooling-container-disk:v1.7.0 "localhost:${port}/alpine-with-test-tooling-container-disk:latest"
  podman push --tls-verify=false "localhost:${port}/alpine-with-test-tooling-container-disk:latest"
fi

kubectl=${PWD}/cluster-up/kubectl.sh

$kubectl get pods -n kubevirt

make test-e2e