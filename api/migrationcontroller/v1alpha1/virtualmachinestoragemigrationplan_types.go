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

const (
	VirtualMachineStorageMigrationPlanKind = "VirtualMachineStorageMigrationPlan"
)

// VirtualMachineStorageMigrationPlanSpec defines the desired state of VirtualMachineStorageMigrationPlan
type VirtualMachineStorageMigrationPlanSpec struct {
	// The virtual machines to migrate.
	VirtualMachines []VirtualMachineStorageMigrationPlanVirtualMachine `json:"virtualMachines"`
}

type VirtualMachineStorageMigrationPlanVirtualMachine struct {
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
	// The name of the virtual machine to migrate.
	Name                string                                                 `json:"name"`
	TargetMigrationPVCs []VirtualMachineStorageMigrationPlanTargetMigrationPVC `json:"targetMigrationPVCs"`
}

type VirtualMachineStorageMigrationPlanTargetMigrationPVC struct {
	VolumeName     string                                           `json:"volumeName"`
	DestinationPVC VirtualMachineStorageMigrationPlanDestinationPVC `json:"destinationPVC"`
}

type VirtualMachineStorageMigrationPlanStatusVirtualMachine struct {
	VirtualMachineStorageMigrationPlanVirtualMachine `json:",inline"`
	SourcePVCs                                       []VirtualMachineStorageMigrationPlanSourcePVC `json:"sourcePVCs"`
}

type VirtualMachineStorageMigrationPlanSourcePVC struct {
	VolumeName string                       `json:"volumeName"`
	Name       string                       `json:"name"`
	Namespace  string                       `json:"namespace"`
	SourcePVC  corev1.PersistentVolumeClaim `json:"sourcePVC"`
}

// +kubebuilder:validation:Enum=ReadWriteOnce;ReadOnlyMany;ReadWriteMany;Auto
type VirtualMachineStorageMigrationPlanAccessMode string

type VirtualMachineStorageMigrationPlanDestinationPVC struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
	// The name of the destination PVC. If not provided, the PVC will be named after the source PVC with a "-mig-xxxx" suffix.
	Name *string `json:"name,omitempty"`
	// +kubebuilder:validation:Optional
	// The target storage class to use for the PVC. If not provided, the PVC will use the default storage class.
	StorageClassName *string `json:"storageClassName,omitempty"`
	// +kubebuilder:validation:Optional
	// The access modes to use for the PVC, if set to Auto, the access mode will be looked up from the storage class storage profile.
	AccessModes []VirtualMachineStorageMigrationPlanAccessMode `json:"accessModes"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Filesystem;Block;Auto
	// The volume mode to use for the PVC, if set to Auto, the volume mode will be looked up from the storage class storage profile. If empty, it will be set to filesystem.
	VolumeMode *corev1.PersistentVolumeMode `json:"volumeMode,omitempty"`
}

// MigPlanStatus defines the observed state of MigPlan
type VirtualMachineStorageMigrationPlanStatus struct {
	// Ready migrations are migrations that are ready to be started.
	ReadyMigrations []VirtualMachineStorageMigrationPlanStatusVirtualMachine `json:"readyMigrations,omitempty"`
	// Invalid migrations are migrations that are invalid and cannot be started.
	InvalidMigrations []VirtualMachineStorageMigrationPlanStatusVirtualMachine `json:"invalidMigrations,omitempty"`
	// InProgress migrations are migrations that are in progress.
	InProgressMigrations []VirtualMachineStorageMigrationPlanStatusVirtualMachine `json:"inProgressMigrations,omitempty"`
	// The migrations that have been completed.
	CompletedMigrations []VirtualMachineStorageMigrationPlanStatusVirtualMachine `json:"completedMigrations,omitempty"`
	// The migrations that have failed.
	FailedMigrations []VirtualMachineStorageMigrationPlanStatusVirtualMachine `json:"failedMigrations,omitempty"`

	// The suffix to automatically append to the PVC name.
	Suffix *string `json:"suffix,omitempty"`
	// The conditions of the migration plan.
	Conditions `json:",inline"`
}

func (r *VirtualMachineStorageMigrationPlan) GetSuffix() string {
	if r.Status.Suffix != nil {
		return *r.Status.Suffix
	}
	return ""
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// VirtualMachineStorageMigrationPlan is the Schema for the virtualmachinestoragemigrationplans API
// +genclient
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type VirtualMachineStorageMigrationPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineStorageMigrationPlanSpec   `json:"spec,omitempty"`
	Status VirtualMachineStorageMigrationPlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualMachineStorageMigrationPlanList contains a list of VirtualMachineStorageMigrationPlan.
type VirtualMachineStorageMigrationPlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachineStorageMigrationPlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VirtualMachineStorageMigrationPlan{}, &VirtualMachineStorageMigrationPlanList{})
}
