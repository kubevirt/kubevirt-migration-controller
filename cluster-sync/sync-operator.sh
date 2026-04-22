#!/bin/bash

set -ex

source ./cluster-up/hack/common.sh
source ./cluster-up/cluster/${KUBEVIRT_PROVIDER}/provider.sh

CERT_MANAGER_VERSION=${CERT_MANAGER_VERSION:-v1.16.3}
OPERATOR_NAMESPACE=${OPERATOR_NAMESPACE:-kubevirt}
OPERATOR_REPO=${OPERATOR_REPO:-quay.io/kubevirt/kubevirt-migration-operator}
CONTROLLER_IMAGE=${CONTROLLER_IMAGE:-quay.io/kubevirt/kubevirt-migration-controller:latest}

# Label to identify all resources created by this script
MANAGED_BY_LABEL="migration.kubevirt.io/managed-by=cluster-sync-operator"

# Determine operator version and branch based on current branch
CURRENT_BRANCH=$(git branch --show-current)
if [[ "$CURRENT_BRANCH" =~ ^release-([0-9]+\.[0-9]+)$ ]]; then
  # We're on a release branch, use the matching operator version
  RELEASE_VERSION="${BASH_REMATCH[1]}"
  OPERATOR_TAG="v${RELEASE_VERSION}.0"
  OPERATOR_BRANCH="release-${RELEASE_VERSION}"
  echo "Detected release branch: ${CURRENT_BRANCH}, using operator tag: ${OPERATOR_TAG}, branch: ${OPERATOR_BRANCH}"
else
  # Use latest operator
  OPERATOR_TAG="latest"
  OPERATOR_BRANCH="main"
  echo "Not on a release branch, using operator tag: ${OPERATOR_TAG}, branch: ${OPERATOR_BRANCH}"
fi

# Allow override via environment variables
OPERATOR_TAG=${OPERATOR_TAG_OVERRIDE:-$OPERATOR_TAG}
OPERATOR_BRANCH=${OPERATOR_BRANCH_OVERRIDE:-$OPERATOR_BRANCH}
OPERATOR_IMAGE="${OPERATOR_REPO}:${OPERATOR_TAG}"

# Verify the operator branch exists, fall back to main if not
OPERATOR_GITHUB_RAW_BASE="https://raw.githubusercontent.com/kubevirt/kubevirt-migration-operator"
if ! curl -sf "${OPERATOR_GITHUB_RAW_BASE}/${OPERATOR_BRANCH}/README.md" > /dev/null 2>&1; then
  echo "Warning: Branch ${OPERATOR_BRANCH} not found in operator repository, falling back to main"
  OPERATOR_BRANCH="main"
fi

echo "Deploying kubevirt-migration-operator from: ${OPERATOR_IMAGE}"
echo "Using RBAC manifests from operator branch: ${OPERATOR_BRANCH}"

# Undeploy any existing kustomize-based deployment
make undeploy || echo "No existing deployment to undeploy"

# Clean up existing operator deployment if present
echo "Cleaning up existing operator resources..."

# Delete all resources with our management label
_kubectl delete all,serviceaccount,role,rolebinding,clusterrole,clusterrolebinding,crd -l "${MANAGED_BY_LABEL}" -n "${OPERATOR_NAMESPACE}" --ignore-not-found=true --timeout=60s 2>/dev/null || true
_kubectl delete clusterrole,clusterrolebinding -l "${MANAGED_BY_LABEL}" --ignore-not-found=true --timeout=60s 2>/dev/null || true
_kubectl delete crd -l "${MANAGED_BY_LABEL}" --ignore-not-found=true --timeout=60s 2>/dev/null || true

# Also delete MigController CRs (may not have our label if created manually)
_kubectl delete migcontroller --all -n "${OPERATOR_NAMESPACE}" --ignore-not-found=true --timeout=60s 2>/dev/null || true

echo "Cleanup complete."

if [[ "$KUBEVIRT_PROVIDER" != "external" ]]; then
  # Install CertManager
  _kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml
  _kubectl wait deployment.apps/cert-manager-webhook --for condition=Available --namespace cert-manager --timeout 5m
fi

# Create operator namespace if it doesn't exist
_kubectl create namespace "${OPERATOR_NAMESPACE}" --dry-run=client -o yaml | _kubectl apply -f -

# Deploy the operator with RBAC and CRDs from upstream repository
# Pulling manifests directly from GitHub to ensure they're always up to date
echo "Fetching manifests from kubevirt-migration-operator repository (branch: ${OPERATOR_BRANCH})..."

RBAC_BASE_URL="${OPERATOR_GITHUB_RAW_BASE}/${OPERATOR_BRANCH}/config/rbac"
OPERATOR_BASE_URL="${OPERATOR_GITHUB_RAW_BASE}/${OPERATOR_BRANCH}/config/operator"
CRD_BASE_URL="${OPERATOR_GITHUB_RAW_BASE}/${OPERATOR_BRANCH}/config/crd/bases"

# Install CRDs first
echo "Installing CRDs..."
curl -sL "${CRD_BASE_URL}/migrations.kubevirt.io_migcontrollers.yaml" | _kubectl apply -f -
_kubectl label crd migcontrollers.migrations.kubevirt.io "${MANAGED_BY_LABEL}" --overwrite || true

# Apply ServiceAccount
echo "Applying ServiceAccount..."
curl -sL "${RBAC_BASE_URL}/service_account.yaml" | _kubectl apply -f -
_kubectl label serviceaccount operator -n "${OPERATOR_NAMESPACE}" "${MANAGED_BY_LABEL}" --overwrite || true

# Apply RBAC roles and bindings
echo "Applying RBAC roles and bindings..."
curl -sL "${RBAC_BASE_URL}/role.yaml" | _kubectl apply -n "${OPERATOR_NAMESPACE}" -f -
_kubectl label clusterrole manager-role "${MANAGED_BY_LABEL}" --overwrite || true
_kubectl label role manager-role -n "${OPERATOR_NAMESPACE}" "${MANAGED_BY_LABEL}" --overwrite || true

curl -sL "${RBAC_BASE_URL}/role_binding.yaml" | _kubectl apply -n "${OPERATOR_NAMESPACE}" -f -
_kubectl label clusterrolebinding manager-rolebinding "${MANAGED_BY_LABEL}" --overwrite || true
_kubectl label rolebinding manager-rolebinding -n "${OPERATOR_NAMESPACE}" "${MANAGED_BY_LABEL}" --overwrite || true

# Apply leader election RBAC (includes permissions for leases)
echo "Applying leader election RBAC..."
curl -sL "${RBAC_BASE_URL}/leader_election_role.yaml" | _kubectl apply -n "${OPERATOR_NAMESPACE}" -f -
_kubectl label role leader-election-role -n "${OPERATOR_NAMESPACE}" "${MANAGED_BY_LABEL}" --overwrite || true

curl -sL "${RBAC_BASE_URL}/leader_election_role_binding.yaml" | _kubectl apply -n "${OPERATOR_NAMESPACE}" -f -
_kubectl label rolebinding leader-election-rolebinding -n "${OPERATOR_NAMESPACE}" "${MANAGED_BY_LABEL}" --overwrite || true

# Apply metrics RBAC
echo "Applying metrics RBAC..."
curl -sL "${RBAC_BASE_URL}/metrics_auth_role.yaml" | _kubectl apply -f -
_kubectl label clusterrole metrics-auth-role "${MANAGED_BY_LABEL}" --overwrite || true

curl -sL "${RBAC_BASE_URL}/metrics_auth_role_binding.yaml" | _kubectl apply -f -
_kubectl label clusterrolebinding metrics-auth-rolebinding "${MANAGED_BY_LABEL}" --overwrite || true

# Fetch and customize the operator deployment
echo "Deploying operator..."
curl -sL "${OPERATOR_BASE_URL}/operator.yaml" | \
  sed "s|name: operator|name: kubevirt-migration-operator|g" | \
  sed "s|image: controller:latest|image: ${OPERATOR_IMAGE}|g" | \
  sed "s|value: \"0.0.1\"|value: \"${OPERATOR_TAG}\"|g" | \
  sed "s|value: \"quay.io/kubevirt/kubevirt-migration-controller:latest\"|value: \"${CONTROLLER_IMAGE}\"|g" | \
  _kubectl apply -f -
_kubectl label deployment kubevirt-migration-operator -n "${OPERATOR_NAMESPACE}" "${MANAGED_BY_LABEL}" --overwrite || true

echo "Waiting for operator deployment to be ready..."
_kubectl wait deployment/kubevirt-migration-operator -n "${OPERATOR_NAMESPACE}" --for condition=Available --timeout=5m

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

# Create MigController CR to deploy the actual migration controller
echo "Creating MigController CR to deploy the migration controller..."
SAMPLES_BASE_URL="${OPERATOR_GITHUB_RAW_BASE}/${OPERATOR_BRANCH}/config/samples"

curl -sL "${SAMPLES_BASE_URL}/migrations_v1alpha1_migcontroller.yaml" | \
  sed "s|name: migcontroller-sample|name: migcontroller|g" | \
  _kubectl apply -n "${OPERATOR_NAMESPACE}" -f -
_kubectl label migcontroller migcontroller -n "${OPERATOR_NAMESPACE}" "${MANAGED_BY_LABEL}" --overwrite || true

echo "Waiting for migration controller deployment to be ready..."
echo "Note: The operator will create CRDs, RBAC, and the controller deployment."
echo "This may take a few minutes..."

# Wait for the operator to create the controller deployment, then wait for it to be available
# Retry kubectl wait until the deployment exists, then wait for Available condition
until _kubectl wait deployment -n "${OPERATOR_NAMESPACE}" -l app.kubernetes.io/name=kubevirt-migration-controller --for=condition=Available --timeout=5m 2>/dev/null; do
  echo "Deployment not ready yet, will retry in 10 seconds..."
  echo "Checking operator status..."
  _kubectl get migcontroller migcontroller -n "${OPERATOR_NAMESPACE}" -o jsonpath='{.status}' 2>/dev/null || echo "MigController status not available yet"
  sleep 10
done
echo "Migration controller deployment is available."

# Give additional time for RBAC to propagate
echo "Waiting for RBAC to propagate..."
sleep 5

echo "=================================================="
echo "Deployment complete!"
echo "Operator image: ${OPERATOR_IMAGE}"
echo "Controller image: ${CONTROLLER_IMAGE}"
echo "Namespace: ${OPERATOR_NAMESPACE}"
echo ""
echo "All resources are labeled with: ${MANAGED_BY_LABEL}"
echo ""
echo "To view all managed resources:"
echo "  kubectl get all,sa,role,rolebinding,crd -l ${MANAGED_BY_LABEL} -n ${OPERATOR_NAMESPACE}"
echo "  kubectl get clusterrole,clusterrolebinding,crd -l ${MANAGED_BY_LABEL}"
echo ""
echo "To delete all managed resources:"
echo "  kubectl delete all,sa,role,rolebinding -l ${MANAGED_BY_LABEL} -n ${OPERATOR_NAMESPACE}"
echo "  kubectl delete clusterrole,clusterrolebinding,crd -l ${MANAGED_BY_LABEL}"
echo ""
echo "If you see RBAC errors in the controller logs:"
echo "  # Check operator logs:"
echo "  kubectl logs -n ${OPERATOR_NAMESPACE} deployment/kubevirt-migration-operator"
echo "  # Check MigController status:"
echo "  kubectl get migcontroller migcontroller -n ${OPERATOR_NAMESPACE} -o yaml"
echo "  # List CRDs created by operator (should include storage migration CRDs):"
echo "  kubectl get crd | grep migrations.kubevirt.io"
echo "=================================================="
