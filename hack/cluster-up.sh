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

KUBEVIRT_DEPLOY_CDI=${KUBEVIRT_DEPLOY_CDI:-true} ./cluster-up/up.sh

source ./cluster-up/hack/common.sh
source ./cluster-up/cluster/${KUBEVIRT_PROVIDER}/provider.sh

# Point at latest KubeVirt release
export KV_RELEASE=${KV_RELEASE:-v1.6.1}

# Deploy the KubeVirt operator
_kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/${KV_RELEASE}/kubevirt-operator.yaml
# Create the KubeVirt CR (instance deployment request) which triggers the actual installation
# _kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/${KV_RELEASE}/kubevirt-cr.yaml
# vmRolloutStrategy is required for storage live migration
_kubectl apply -f - <<EOF
apiVersion: kubevirt.io/v1
kind: KubeVirt
metadata:
  name: kubevirt
  namespace: kubevirt
spec:
  certificateRotateStrategy: {}
  configuration:
    network:
      defaultNetworkInterface: masquerade
    vmRolloutStrategy: LiveUpdate
    developerConfiguration:
      featureGates:
      - ExpandDisks
  customizeComponents: {}
  imagePullPolicy: IfNotPresent
  workloadUpdateStrategy:
    workloadUpdateMethods:
    - LiveMigrate
EOF
# wait until all KubeVirt components are up
_kubectl -n kubevirt wait kv kubevirt --for condition=Available --timeout 15m
