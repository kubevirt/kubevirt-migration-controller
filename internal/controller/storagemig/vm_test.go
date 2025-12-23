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
package storagemig

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
)

const (
	testVM           = "test-vm"
	testStorageClass = "test-storage-class"
	testVolume       = "test-volume"
	testSourceDV     = "test-source-dv"
	testTargetDV     = "test-target-dv"
	testNamespace    = "test-namespace"
)

var _ = Describe("StorageMigration VM", func() {
	var (
		controllerReconciler *StorageMigrationReconciler
	)
	ctx := context.Background()

	BeforeEach(func() {
		controllerReconciler = &StorageMigrationReconciler{
			Client:         k8sClient,
			UncachedClient: k8sClient,
			Scheme:         k8sClient.Scheme(),
		}
	})

	AfterEach(func() {
		if controllerReconciler != nil {
			CleanupResources(ctx, controllerReconciler.Client)
			controllerReconciler = nil
		}
	})

	It("should return an error if the vm is nil or empty", func() {
		t := &Task{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		Expect(t.updateVMForStorageMigration(ctx, nil, migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{})).To(MatchError("vm is nil or empty"))
		Expect(t.updateVMForStorageMigration(ctx, &virtv1.VirtualMachine{}, migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{})).To(MatchError("vm is nil or empty"))
	})

	DescribeTable("should update the VM for storage migration with a dv template in the vm", func(datavolumeSpec *cdiv1.DataVolumeSpec) {
		vm := createVirtualMachineWithDVTemplate(testVM, datavolumeSpec)
		Expect(k8sClient.Create(ctx, vm)).To(Succeed())
		t := &Task{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		planVM := migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
			SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
				{
					VolumeName: testVolume,
					Name:       testSourceDV,
					Namespace:  testNamespace,
					SourcePVC:  corev1.PersistentVolumeClaim{},
				},
			},
			VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
				Name: testVM,
				TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
					{
						VolumeName: testVolume,
						DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
							Name:             ptr.To(testTargetDV),
							StorageClassName: ptr.To(testStorageClass),
						},
					},
				},
			},
		}
		Expect(t.updateVMForStorageMigration(ctx, vm, planVM)).To(Succeed())
		UpdatedVM := &virtv1.VirtualMachine{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testVM}, UpdatedVM)).To(Succeed())
		Expect(UpdatedVM.Spec.UpdateVolumesStrategy).ToNot(BeNil())
		Expect(*UpdatedVM.Spec.UpdateVolumesStrategy).To(Equal(virtv1.UpdateVolumesStrategyMigration))
		Expect(UpdatedVM.Spec.Template.Spec.Volumes).To(HaveLen(1))
		Expect(UpdatedVM.Spec.Template.Spec.Volumes[0].Name).To(Equal(testVolume))
		Expect(UpdatedVM.Spec.Template.Spec.Volumes[0].VolumeSource.DataVolume).ToNot(BeNil())
		Expect(UpdatedVM.Spec.Template.Spec.Volumes[0].VolumeSource.DataVolume.Name).To(Equal(testTargetDV))
		Expect(UpdatedVM.Spec.DataVolumeTemplates).To(HaveLen(1))
		Expect(UpdatedVM.Spec.DataVolumeTemplates[0].Name).To(Equal(testTargetDV))
		if datavolumeSpec.Storage != nil {
			Expect(UpdatedVM.Spec.DataVolumeTemplates[0].Spec.Storage).ToNot(BeNil())
			Expect(UpdatedVM.Spec.DataVolumeTemplates[0].Spec.Storage.StorageClassName).ToNot(BeNil())
			Expect(*UpdatedVM.Spec.DataVolumeTemplates[0].Spec.Storage.StorageClassName).To(Equal(testStorageClass))
		} else if datavolumeSpec.PVC != nil {
			Expect(UpdatedVM.Spec.DataVolumeTemplates[0].Spec.PVC).ToNot(BeNil())
			Expect(UpdatedVM.Spec.DataVolumeTemplates[0].Spec.PVC.StorageClassName).ToNot(BeNil())
			Expect(*UpdatedVM.Spec.DataVolumeTemplates[0].Spec.PVC.StorageClassName).To(Equal(testStorageClass))
		}
	},
		Entry("storage spec", &cdiv1.DataVolumeSpec{
			Storage: &cdiv1.StorageSpec{
				StorageClassName: ptr.To(testStorageClass),
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}),
		Entry("pvc spec", &cdiv1.DataVolumeSpec{
			PVC: &corev1.PersistentVolumeClaimSpec{
				StorageClassName: ptr.To(testStorageClass),
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}),
	)
})

func createVirtualMachineWithDVTemplate(name string, datavolumeSpec *cdiv1.DataVolumeSpec) *virtv1.VirtualMachine {
	return &virtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: virtv1.VirtualMachineSpec{
			DataVolumeTemplates: []virtv1.DataVolumeTemplateSpec{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: testSourceDV,
					},
					Spec: *datavolumeSpec,
				},
			},
			Template: &virtv1.VirtualMachineInstanceTemplateSpec{
				Spec: virtv1.VirtualMachineInstanceSpec{
					Volumes: []virtv1.Volume{
						{
							Name: testVolume,
							VolumeSource: virtv1.VolumeSource{
								DataVolume: &virtv1.DataVolumeSource{
									Name: testSourceDV,
								},
							},
						},
					},
				},
			},
		},
	}
}
