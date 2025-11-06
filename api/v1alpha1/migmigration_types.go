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
	MigMigrationKind      = "MigMigration"
	MigMigrationFinalizer = "migmigration.kubevirt.io/finalizer"
)

// MigMigrationSpec defines the desired state of MigMigration
type MigMigrationSpec struct {
	MigPlanRef *corev1.ObjectReference `json:"migPlanRef,omitempty"`
}

// MigMigrationStatus defines the observed state of MigMigration
type MigMigrationStatus struct {
	Conditions `json:",inline"`
	Phase      string   `json:"phase,omitempty"`
	Itinerary  string   `json:"itinerary,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// MigMigration is the Schema for the migmigrations API
// +k8s:openapi-gen=true
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Plan",type=string,JSONPath=".spec.migPlanRef.name"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type MigMigration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MigMigrationSpec   `json:"spec,omitempty"`
	Status MigMigrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MigMigrationList contains a list of MigMigration.
type MigMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MigMigration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MigMigration{}, &MigMigrationList{})
}

// Add (de-duplicated) errors.
func (r *MigMigration) AddErrors(errors []string) {
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
func (r *MigMigration) HasErrors() bool {
	return len(r.Status.Errors) > 0
}

// FindOwnerReference finds the owner reference of the migration
func (r *MigMigration) FindOwnerReference() *metav1.OwnerReference {
	for _, ownerReference := range r.OwnerReferences {
		if ownerReference.Kind == MigPlanKind && ownerReference.APIVersion == GroupVersion.Version {
			return &ownerReference
		}
	}
	return nil
}

// SetFinalizer adds the passed in finalizer to the migration
func (r *MigMigration) AddFinalizer(finalizer string) {
	r.Finalizers = append(r.Finalizers, finalizer)
}

// RemoveFinalizer removes the passed in finalizer from the migration
func (r *MigMigration) RemoveFinalizer(finalizer string) {
	for i, f := range r.Finalizers {
		if f == finalizer {
			r.Finalizers = append(r.Finalizers[:i], r.Finalizers[i+1:]...)
			break
		}
	}
}
