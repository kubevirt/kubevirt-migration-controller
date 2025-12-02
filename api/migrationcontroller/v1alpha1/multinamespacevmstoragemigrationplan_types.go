/*
Copyright 2025 The KubeVirt Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	MultiNamespaceVirtualMachineStorageMigrationPlanKind = "MultiNamespaceVirtualMachineStorageMigrationPlan"
)

// VirtualMachineStorageMigrationPlanSpec defines the desired state of VirtualMachineStorageMigrationPlan
type MultiNamespaceVirtualMachineStorageMigrationPlanSpec struct {
	// The virtual machines to migrate.
	Namespaces []VirtualMachineStorageMigrationPlanNamespaceSpec `json:"namespaces"`
}

type VirtualMachineStorageMigrationPlanNamespaceSpec struct {
	// The name of the namespace to migrate.
	Name string `json:"name"`
	// The virtual machines storage migration plan spec for the namespace.
	*VirtualMachineStorageMigrationPlanSpec `json:",inline"`
}

// MultiNamespaceVirtualMachineStorageMigrationPlanStatus defines the observed state of MultiNamespaceVirtualMachineStorageMigrationPlan
type MultiNamespaceVirtualMachineStorageMigrationPlanStatus struct {
	// The status of the plans in the namespaces.
	Namespaces []VirtualMachineStorageMigrationPlanNamespaceStatus `json:"namespaces,omitempty"`
	// The conditions of the multi-namespace virtual machine storage migration plan.
	Conditions `json:",inline"`
}

// VirtualMachineStorageMigrationPlanNamespaceStatus defines the status of the plan in the namespace.
type VirtualMachineStorageMigrationPlanNamespaceStatus struct {
	// The name of the namespace to migrate.
	Name string `json:"name"`
	// The status of the plan in the namespace.
	*VirtualMachineStorageMigrationPlanStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MultiNamespaceVirtualMachineStorageMigrationPlan is the Schema for the multinamespacevmstoragemigrationplans API
// +genclient
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type MultiNamespaceVirtualMachineStorageMigrationPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MultiNamespaceVirtualMachineStorageMigrationPlanSpec   `json:"spec,omitempty"`
	Status MultiNamespaceVirtualMachineStorageMigrationPlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MultiNamespaceVirtualMachineStorageMigrationPlanList contains a list of MultiNamespaceVirtualMachineStorageMigrationPlan.
type MultiNamespaceVirtualMachineStorageMigrationPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MultiNamespaceVirtualMachineStorageMigrationPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MultiNamespaceVirtualMachineStorageMigrationPlan{}, &MultiNamespaceVirtualMachineStorageMigrationPlanList{})
}

func GetNamespacedPlanName(planName, namespace string) string {
	return planName + "-" + namespace
}
