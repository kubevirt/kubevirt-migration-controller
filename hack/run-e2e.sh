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
  $kubectl delete -f "${REPO_ROOT}/nginx-proxy/nginx-ca.yaml"
  $kubectl delete -f "${REPO_ROOT}/nginx-proxy/nginx-cm.yaml"
  $kubectl delete -f "${REPO_ROOT}/nginx-proxy/nginx-secret.yaml"
  $kubectl delete -f "${REPO_ROOT}/nginx-proxy/nginx-svc.yaml"
  $kubectl delete -f "${REPO_ROOT}/nginx-proxy/nginx-deployment.yaml"
}


# deploy nginx registry proxy in the default namespace
# so we can access the same container over and over
# using the proxy
kubectl="${REPO_ROOT}/cluster-up/kubectl.sh"
$kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-ca.yaml"
$kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-cm.yaml"
$kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-secret.yaml"
$kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-svc.yaml"
$kubectl apply -f "${REPO_ROOT}/nginx-proxy/nginx-deployment.yaml"

trap 'cleanup' EXIT


$kubectl get pods -n kubevirt

make test-e2e
