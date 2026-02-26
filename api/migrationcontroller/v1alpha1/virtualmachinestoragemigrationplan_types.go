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

// RetentionPolicy defines what to do with the source DataVolume/PVC after a migration completes.
// +kubebuilder:validation:Enum=keepSource;deleteSource
type RetentionPolicy string

const (
	// RetentionPolicyKeepSource keeps the source DataVolume/PVC after migration (default behavior).
	RetentionPolicyKeepSource RetentionPolicy = "keepSource"
	// RetentionPolicyDeleteSource deletes the source DataVolume (if it exists) or source PVC after migration completes.
	RetentionPolicyDeleteSource RetentionPolicy = "deleteSource"
)

// VirtualMachineStorageMigrationPlanSpec defines the desired state of VirtualMachineStorageMigrationPlan
type VirtualMachineStorageMigrationPlanSpec struct {
	// The virtual machines to migrate.
	VirtualMachines []VirtualMachineStorageMigrationPlanVirtualMachine `json:"virtualMachines"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=keepSource
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="retentionPolicy is immutable"
	// RetentionPolicy indicates whether to keep or delete the source DataVolume/PVC after each VM migration completes.
	// When "keepSource" (default), the source is preserved. When "deleteSource", the source DataVolume is deleted
	// if it exists, otherwise the source PVC is deleted.
	RetentionPolicy *RetentionPolicy `json:"retentionPolicy,omitempty"`
}

// VirtualMachineStorageMigrationPlanVirtualMachine defines the VirtualMachine to migrate and the PVCs to migrate.
type VirtualMachineStorageMigrationPlanVirtualMachine struct {
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
	// The name of the virtual machine to migrate.
	Name string `json:"name"`
	// A list of PVCs associated with the VirtualMachine to migrate.
	TargetMigrationPVCs []VirtualMachineStorageMigrationPlanTargetMigrationPVC `json:"targetMigrationPVCs"`
}

// VirtualMachineStorageMigrationPlanTargetMigrationPVC defines the PVC to migrate to.
type VirtualMachineStorageMigrationPlanTargetMigrationPVC struct {
	// The name of the volume in the VirtualMachine to migrate.
	VolumeName string `json:"volumeName"`
	// The destination PVC to migrate to.
	DestinationPVC VirtualMachineStorageMigrationPlanDestinationPVC `json:"destinationPVC"`
}

// VirtualMachineStorageMigrationPlanStatusVirtualMachine defines the status of the VirtualMachine to migrate.
type VirtualMachineStorageMigrationPlanStatusVirtualMachine struct {
	// The VirtualMachine to migrate.
	VirtualMachineStorageMigrationPlanVirtualMachine `json:",inline"`
	// A list of source PVCs currently used by the VirtualMachine.
	SourcePVCs []VirtualMachineStorageMigrationPlanSourcePVC `json:"sourcePVCs"`
}

// VirtualMachineStorageMigrationPlanSourcePVC defines the source PVC used by the VirtualMachine.
type VirtualMachineStorageMigrationPlanSourcePVC struct {
	// The name of the volume in the VirtualMachine.
	VolumeName string `json:"volumeName"`
	// The name of the source PVC.
	Name string `json:"name"`
	// The namespace of the source PVC.
	Namespace string `json:"namespace"`
	// The source PVC.
	SourcePVC corev1.PersistentVolumeClaim `json:"sourcePVC"`
}

// +kubebuilder:validation:Enum=ReadWriteOnce;ReadOnlyMany;ReadWriteMany;Auto
// The access mode of the source PVC. If set to Auto, the access mode will be looked up from the storage class storage profile.
type VirtualMachineStorageMigrationPlanAccessMode string

// VirtualMachineStorageMigrationPlanDestinationPVC the definition of the destination PVC.
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

// VirtualMachineStorageMigrationPlanStatus defines the observed state of VirtualMachineStorageMigrationPlan
type VirtualMachineStorageMigrationPlanStatus struct {
	// The number of virtual machines that have been completed out of the total number of virtual machines.
	CompletedOutOf string `json:"completedOutOf,omitempty"`
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

	// The suffix to automatically append to the source PVC name. If the target name is not provided. This will replace the suffix "-new" or "-mig-xxxx" if present on the source PVC name.
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
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Progressing",type=string,JSONPath=".status.conditions[?(@.type=='Progressing')].status"
// +kubebuilder:printcolumn:name="Completed VMs",type=string,JSONPath=".status.completedOutOf"
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
