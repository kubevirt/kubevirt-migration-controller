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
	MigPlanKind = "MigPlan"
)

// MigPlanSpec defines the desired state of MigPlan
type MigPlanSpec struct {
	// The virtual machines to migrate.
	VirtualMachines []MigPlanVirtualMachine `json:"virtualMachines"`
}

type MigPlanVirtualMachine struct {
	Name          string                `json:"name"`
	MigrationPVCs []MigPlanMigrationPVC `json:"migrationPVCs"`
}

type MigPlanMigrationPVC struct {
	SourceName     string                `json:"name"`
	DestinationPVC MigPlanDestinationPVC `json:"destinationPVC"`
}

type MigPlanDestinationPVC struct {
	Name string `json:"name"`
	// +kubebuilder:validation:Optional
	// The target storage class to use for the PVC. If not provided, the PVC will use the default storage class.
	StorageClass string `json:"storageClass,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadOnlyMany;ReadWriteMany;Auto
	// The access modes to use for the PVC, if set to Auto, the access mode will be looked up from the storage class storage profile.
	AccessModes []string `json:"accessModes"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Filesystem;Block;Auto
	// The volume mode to use for the PVC, if set to Auto, the volume mode will be looked up from the storage class storage profile. If empty, it will be set to filesystem.
	VolumeMode string `json:"volumeMode,omitempty"`
}

// MigPlanStatus defines the observed state of MigPlan
type MigPlanStatus struct {
	// The migrations that have been completed.
	CompletedMigrations []MigPlanVirtualMachine `json:"completedMigrations,omitempty"`
	// The migrations that have failed.
	FailedMigrations []MigPlanVirtualMachine `json:"failedMigrations,omitempty"`

	// The suffix to automatically append to the PVC name.
	Suffix *string `json:"suffix,omitempty"`
	// The current phase of the migration plan.
	Phase string `json:"phase,omitempty"`
	// The conditions of the migration plan.
	Conditions `json:",inline"`
}

func (r *MigPlan) GetSuffix() string {
	if r.Status.Suffix != nil {
		return *r.Status.Suffix
	}
	return ""
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MigPlan is the Schema for the migplans API
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type MigPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MigPlanSpec   `json:"spec,omitempty"`
	Status MigPlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MigPlanList contains a list of MigPlan.
type MigPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MigPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MigPlan{}, &MigPlanList{})
}
