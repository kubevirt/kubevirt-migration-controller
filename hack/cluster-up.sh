#!/bin/bash

set -ex

KUBEVIRT_DEPLOY_CDI=${KUBEVIRT_DEPLOY_CDI:-true} ./cluster-up/up.sh

source ./cluster-up/hack/common.sh
source ./cluster-up/cluster/${KUBEVIRT_PROVIDER}/provider.sh

# Point at latest KubeVirt release
export KV_RELEASE=v1.5.2
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
