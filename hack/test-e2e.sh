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

# Function to handle cleanup
cleanup() {
  $kubectl delete -f nginx-proxy/nginx-ca.yaml
  $kubectl delete -f nginx-proxy/nginx-cm.yaml
  $kubectl delete -f nginx-proxy/nginx-secret.yaml
  $kubectl delete -f nginx-proxy/nginx-svc.yaml
  $kubectl delete -f nginx-proxy/nginx-deployment.yaml
}


# deploy nginx registry proxy in the default namespace
# so we can access the same container over and over
# using the proxy
kubectl=${PWD}/cluster-up/kubectl.sh
$kubectl apply -f nginx-proxy/nginx-ca.yaml
$kubectl apply -f nginx-proxy/nginx-cm.yaml
$kubectl apply -f nginx-proxy/nginx-secret.yaml
$kubectl apply -f nginx-proxy/nginx-svc.yaml
$kubectl apply -f nginx-proxy/nginx-deployment.yaml

trap 'cleanup' EXIT


$kubectl get pods -n kubevirt

make test-e2e
