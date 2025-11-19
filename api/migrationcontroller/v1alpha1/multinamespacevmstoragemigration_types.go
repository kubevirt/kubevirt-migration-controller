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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MigMigrationSpec defines the desired state of MigMigration
type MultiNamespaceVirtualMachineStorageMigrationSpec struct {
	MultiNamespaceVirtualMachineStorageMigrationPlanRef *corev1.ObjectReference `json:"multiNamespaceVirtualMachineStorageMigrationPlanRef"`
}

// MultiNamespaceVirtualMachineStorageMigrationStatus defines the observed state of MultiNamespaceVirtualMachineStorageMigration
type MultiNamespaceVirtualMachineStorageMigrationStatus struct {
	// The conditions of the multi-namespace migration in the namespace.
	Conditions `json:",inline"`
	// The status of the migrations in the namespaces.
	Namespaces []MultiNamespaceVirtualMachineStorageMigrationNamespaceStatus `json:"namespaces,omitempty"`
}

type MultiNamespaceVirtualMachineStorageMigrationNamespaceStatus struct {
	// The name of the namespace to migrate.
	Name string `json:"name"`
	// The status of the migration in the namespace.
	*VirtualMachineStorageMigrationStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// VirtualMachineStorageMigration is the Schema for the virtualmachinestoragemigrations API
// +k8s:openapi-gen=true
// +genclient
// +kubebuilder:printcolumn:name="Plan",type=string,JSONPath=".spec.virtualMachineStorageMigrationPlanRef.name"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type MultiNamespaceVirtualMachineStorageMigration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MultiNamespaceVirtualMachineStorageMigrationSpec   `json:"spec,omitempty"`
	Status MultiNamespaceVirtualMachineStorageMigrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MultiNamespaceVirtualMachineStorageMigrationList contains a list of MultiNamespaceVirtualMachineStorageMigration.
type MultiNamespaceVirtualMachineStorageMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MultiNamespaceVirtualMachineStorageMigration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MultiNamespaceVirtualMachineStorageMigration{}, &MultiNamespaceVirtualMachineStorageMigrationList{})
}
