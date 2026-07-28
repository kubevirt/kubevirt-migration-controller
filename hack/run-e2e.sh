#!/bin/bash
# Copyright 2026 The KubeVirt Authors.
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

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"

# Function to handle cleanup
cleanup() {
  if [ "${CREATED_NGINX_PROXY}" = "true" ]; then
    $kubectl delete namespace nginx-proxy --ignore-not-found
  fi
}


# Deploy nginx registry proxy into the nginx-proxy namespace if not already
# present (cluster-sync deploys it, but run-e2e.sh may be invoked standalone).
kubectl="${REPO_ROOT}/cluster-up/kubectl.sh"
CREATED_NGINX_PROXY=false
if ! $kubectl get namespace nginx-proxy &> /dev/null; then
  CREATED_NGINX_PROXY=true
  $kubectl create namespace nginx-proxy
  $kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-ca.yaml" -n nginx-proxy
  $kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-cm.yaml" -n nginx-proxy
  $kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-secret.yaml" -n nginx-proxy
  $kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-svc.yaml" -n nginx-proxy
  $kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-deployment.yaml" -n nginx-proxy
  $kubectl rollout status -n nginx-proxy deployment/nginx-registry-proxy --timeout=120s
fi

trap 'cleanup' EXIT


$kubectl get pods -n kubevirt

make test-e2e
