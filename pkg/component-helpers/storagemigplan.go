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
package componenthelpers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
)

const (
	DefaultK8sStorageClass  = "storageclass.kubernetes.io/is-default-class"
	DefaultVirtStorageClass = "storageclass.kubevirt.io/is-default-virt-class"
)

// Get a referenced VirtualMachineStorageMigrationPlan.
// Returns `nil` when the reference cannot be resolved.
func GetStorageMigrationPlan(ctx context.Context,
	client k8sclient.Client,
	ref *corev1.ObjectReference) (*migrations.VirtualMachineStorageMigrationPlan, error) {
	if ref == nil {
		return nil, nil
	}
	migPlan := &migrations.VirtualMachineStorageMigrationPlan{}

	if err := client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, migPlan); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	// The kind and api version checks are needed because the object is not set up correctly in the test environment.
	// TODO: remove this once the object is set up correctly in the test environment.
	if migPlan.Kind == "" {
		migPlan.Kind = migrations.VirtualMachineStorageMigrationPlanKind
	}
	if migPlan.APIVersion == "" {
		migPlan.APIVersion = migrations.GroupVersion.String()
	}
	return migPlan, nil
}

// Get a referenced VirtualMachineStorageMigrationPlan.
// Returns `nil` when the reference cannot be resolved.
func GetMultiNamespaceStorageMigrationPlan(ctx context.Context,
	client k8sclient.Client,
	ref *corev1.ObjectReference) (*migrations.MultiNamespaceVirtualMachineStorageMigrationPlan, error) {
	if ref == nil {
		return nil, nil
	}
	migPlan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}

	if err := client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, migPlan); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	// The kind and api version checks are needed because the object is not set up correctly in the test environment.
	// TODO: remove this once the object is set up correctly in the test environment.
	if migPlan.Kind == "" {
		migPlan.Kind = migrations.MultiNamespaceVirtualMachineStorageMigrationPlanKind
	}
	if migPlan.APIVersion == "" {
		migPlan.APIVersion = migrations.GroupVersion.String()
	}
	return migPlan, nil
}

func GetDefaultStorageClass(ctx context.Context, client k8sclient.Client) (*string, error) {
	scList := &storagev1.StorageClassList{}
	if err := client.List(ctx, scList); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, sc := range scList.Items {
		if sc.Annotations != nil && sc.Annotations[DefaultK8sStorageClass] == "true" {
			return &sc.Name, nil
		}
	}
	return nil, nil
}

func GetDefaultVirtStorageClass(ctx context.Context, client k8sclient.Client) (*string, error) {
	scList := &storagev1.StorageClassList{}
	if err := client.List(ctx, scList); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	for _, sc := range scList.Items {
		if sc.Annotations != nil && sc.Annotations[DefaultVirtStorageClass] == "true" {
			return &sc.Name, nil
		}
	}
	return nil, nil
}
