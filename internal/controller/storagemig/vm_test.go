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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
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
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
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

var _ = Describe("StorageMigration DV Size Determination", func() {
	var (
		t *Task
	)
	ctx := context.Background()

	BeforeEach(func() {
		t = &Task{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			Log:    logf.Log.WithName("test"),
			Owner: &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-migration",
					Namespace: testNamespace,
					UID:       types.UID("test-uid"),
				},
			},
			Plan: &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-plan",
					Namespace: testNamespace,
				},
			},
		}
	})

	AfterEach(func() {
		CleanupResources(ctx, k8sClient)
	})

	DescribeTable("should determine correct size for target DV based on source PVC and DV",
		func(pvcVolumeMode *corev1.PersistentVolumeMode, pvcCapacity, expectedSize resource.Quantity) {
			dvSpec := &cdiv1.DataVolumeSpec{
				Source: &cdiv1.DataVolumeSource{Snapshot: &cdiv1.DataVolumeSourceSnapshot{}},
				Storage: &cdiv1.StorageSpec{
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("5Gi"),
						},
					},
				},
			}
			vm := createVirtualMachineWithDVTemplate(testVM, dvSpec)
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
			sourceDV := &cdiv1.DataVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testSourceDV,
					Namespace: testNamespace,
				},
				Spec: *dvSpec,
			}
			Expect(k8sClient.Create(ctx, sourceDV)).To(Succeed())

			sourcePVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testSourceDV,
					Namespace: testNamespace,
					Labels: map[string]string{
						"test": "label",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "cdi.kubevirt.io/v1beta1",
							Kind:       "DataVolume",
							Name:       testSourceDV,
							UID:        sourceDV.UID,
						},
					},
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					VolumeMode:  pvcVolumeMode,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, sourcePVC)).To(Succeed())
			sourcePVC.Status.Capacity = corev1.ResourceList{
				corev1.ResourceStorage: pvcCapacity,
			}
			Expect(k8sClient.Status().Update(ctx, sourcePVC)).To(Succeed())

			// ObjectMeta is pruned on the embedded corev1.PersistentVolumeClaim in the planVM
			// Simulate here as well
			prunedSourcePVC := sourcePVC.DeepCopy()
			prunedSourcePVC.ObjectMeta = metav1.ObjectMeta{}

			planVM := migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
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
				SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
					{
						VolumeName: testVolume,
						Name:       testSourceDV,
						Namespace:  testNamespace,
						SourcePVC:  *prunedSourcePVC,
					},
				},
			}
			Expect(t.liveMigrateVM(ctx, planVM)).To(Succeed())

			targetDV := &cdiv1.DataVolume{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testTargetDV}, targetDV)).To(Succeed())
			Expect(targetDV.Spec.Storage).ToNot(BeNil())
			actualSize := targetDV.Spec.Storage.Resources.Requests[corev1.ResourceStorage]
			Expect(actualSize.Cmp(expectedSize)).To(Equal(0), "target DV expected size %s but got %s", expectedSize.String(), actualSize.String())
			Expect(targetDV.Labels).To(HaveKeyWithValue("test", "label"))
		},
		Entry("DV with Storage spec and Filesystem PVC uses DV storage request size",
			ptr.To(corev1.PersistentVolumeFilesystem),
			resource.MustParse("10Gi"),
			resource.MustParse("5Gi"),
		),
		Entry("DV with Storage spec and Block PVC uses PVC status capacity",
			ptr.To(corev1.PersistentVolumeBlock),
			resource.MustParse("10Gi"),
			resource.MustParse("10Gi"),
		),
	)
})
