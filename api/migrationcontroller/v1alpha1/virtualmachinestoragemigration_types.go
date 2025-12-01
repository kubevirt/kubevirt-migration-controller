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
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	VirtualMachineStorageMigrationUIDLabel  = "virtualmachinestoragemigration.kubevirt.io/virtualmachinestoragemigration-uid"
	VirtualMachineStorageMigrationPlanLabel = "virtualmachinestoragemigration.kubevirt.io/virtualmachinestoragemigrationplan-name"
	VirtualMachineStorageMigrationFinalizer = "virtualmachinestoragemigration.kubevirt.io/finalizer"
)

// MigMigrationSpec defines the desired state of MigMigration
type VirtualMachineStorageMigrationSpec struct {
	VirtualMachineStorageMigrationPlanRef *corev1.ObjectReference `json:"virtualMachineStorageMigrationPlanRef"`
}

// Phases
const (
	Started                                      Phase = "Started"
	RefreshStorageMigrationPlan                  Phase = "RefreshStorageMigrationPlan"
	WaitForStorageMigrationPlanRefreshCompletion Phase = "WaitForStorageMigrationPlanRefreshCompletion"
	BeginLiveMigration                           Phase = "BeginLiveMigration"
	WaitForLiveMigrationToComplete               Phase = "WaitForLiveMigrationToComplete"
	LiveMigrationFailed                          Phase = "LiveMigrationFailed"
	Canceling                                    Phase = "Canceling"
	Canceled                                     Phase = "Canceled"
	CleanupCancelledMigrations                   Phase = "CleanupCancelledMigrations"
	Completed                                    Phase = "Completed"
	CleanupMigrationResources                    Phase = "CleanupMigrationResources"
)

// Phase defines phase of the migration
type Phase string

// VirtualMachineStorageMigrationStatus defines the observed state of VirtualMachineStorageMigration
type VirtualMachineStorageMigrationStatus struct {
	// The conditions of the migration.
	Conditions `json:",inline"`
	// The current phase of the migration.
	Phase Phase `json:"phase,omitempty"`
	// The errors occurred during the migration.
	Errors []string `json:"errors,omitempty"`
	// The running migrations.
	RunningMigrations []RunningVirtualMachineMigration `json:"runningMigrations,omitempty"`
	// The completed migrations.
	CompletedMigrations []string `json:"completedMigrations,omitempty"`
	// The cancelled migrations.
	CancelledMigrations []string `json:"cancelledMigrations,omitempty"`
}

// RunningVirtualMachineMigration has the name of the VirtualMachine and the progress of the migration.
type RunningVirtualMachineMigration struct {
	// The name of the VirtualMachine.
	Name string `json:"name"`
	// The progress of the migration.
	Progress string `json:"progress,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// VirtualMachineStorageMigration is the Schema for the virtualmachinestoragemigrations API
// +k8s:openapi-gen=true
// +genclient
// +kubebuilder:printcolumn:name="Plan",type=string,JSONPath=".spec.virtualMachineStorageMigrationPlanRef.name"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type VirtualMachineStorageMigration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VirtualMachineStorageMigrationSpec   `json:"spec,omitempty"`
	Status VirtualMachineStorageMigrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VirtualMachineStorageMigrationList contains a list of VirtualMachineStorageMigration.
type VirtualMachineStorageMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VirtualMachineStorageMigration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VirtualMachineStorageMigration{}, &VirtualMachineStorageMigrationList{})
}

// Add (de-duplicated) errors.
func (r *VirtualMachineStorageMigration) AddErrors(errors []string) {
	m := map[string]bool{}
	for _, e := range r.Status.Errors {
		m[e] = true
	}
	for _, error := range errors {
		_, found := m[error]
		if !found {
			r.Status.Errors = append(r.Status.Errors, error)
		}
	}
}

// HasErrors will notify about error presence on the MigMigration resource
func (r *VirtualMachineStorageMigration) HasErrors() bool {
	return len(r.Status.Errors) > 0
}

// FindOwnerReference finds the owner reference of the migration
func (r *VirtualMachineStorageMigration) FindOwnerReference() *metav1.OwnerReference {
	for _, ownerReference := range r.OwnerReferences {
		if ownerReference.Kind == VirtualMachineStorageMigrationPlanKind && ownerReference.APIVersion == GroupVersion.String() {
			return &ownerReference
		}
	}
	return nil
}

// HasFinalizer returns true if a resource has a specific finalizer
func HasFinalizer(object metav1.Object, value string) bool {
	for _, f := range object.GetFinalizers() {
		if f == value {
			return true
		}
	}
	return false
}

// SetFinalizer adds the passed in finalizer to the migration
func (r *VirtualMachineStorageMigration) AddFinalizer(finalizer string, log logr.Logger) {
	if HasFinalizer(r, finalizer) {
		log.V(5).Info("Finalizer already exists", "finalizer", finalizer)
		return
	}
	r.Finalizers = append(r.Finalizers, finalizer)
	log.V(5).Info("Added finalizer", "finalizer", finalizer)
}

// RemoveFinalizer removes the passed in finalizer from the migration
func (r *VirtualMachineStorageMigration) RemoveFinalizer(finalizer string, log logr.Logger) {
	for i, f := range r.Finalizers {
		if f == finalizer {
			r.Finalizers = append(r.Finalizers[:i], r.Finalizers[i+1:]...)
			log.V(5).Info("Removed finalizer", "finalizer", finalizer)
			break
		}
	}
}
