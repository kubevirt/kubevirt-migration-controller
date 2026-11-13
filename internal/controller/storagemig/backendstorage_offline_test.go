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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	backendTargetSC     = "target-storage-class"
	backendSourceSC     = "source-storage-class"
	origBackendPVCName  = "persistent-state-for-test-vm-orig"
	cloneBackendPVCName = "persistent-state-for-test-vm-mig-abcd"
)

func backendStorageVM() *virtv1.VirtualMachine {
	return &virtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: testVM, Namespace: testNamespace},
		Spec: virtv1.VirtualMachineSpec{
			Template: &virtv1.VirtualMachineInstanceTemplateSpec{
				Spec: virtv1.VirtualMachineInstanceSpec{
					Domain: virtv1.DomainSpec{
						Devices: virtv1.Devices{
							TPM: &virtv1.TPMDevice{Persistent: ptr.To(true)},
						},
					},
				},
			},
		},
	}
}

func backendStoragePVC(name, sc string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{componenthelpers.BackendStoragePVCLabel: testVM},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Mi")}},
			StorageClassName: ptr.To(sc),
		},
	}
}

func backendPlanVM() migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine {
	return migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
		VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
			Name: testVM,
			TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
				{VolumeName: testVolume, DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{Name: ptr.To(testTargetDV), StorageClassName: ptr.To(backendTargetSC)}},
			},
		},
	}
}

func backendTask(planVM migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) *Task {
	return &Task{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
		Log:    logf.Log.WithName("test"),
		Owner:  &migrations.VirtualMachineStorageMigration{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace}},
		Plan: &migrations.VirtualMachineStorageMigrationPlan{
			ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: testNamespace},
			Status: migrations.VirtualMachineStorageMigrationPlanStatus{
				Suffix:          ptr.To("abcd"),
				ReadyMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{planVM},
			},
		},
	}
}

// expectPVCDeletedOrGone accepts either a missing PVC or one marked for deletion. envtest enables
// StorageObjectInUseProtection, so a deleted PVC keeps its pvc-protection finalizer (no controller
// removes it) and lingers with a DeletionTimestamp instead of disappearing.
func expectPVCDeletedOrGone(ctx context.Context, name string) {
	pvc := &corev1.PersistentVolumeClaim{}
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, pvc)
	if err == nil {
		Expect(pvc.DeletionTimestamp).NotTo(BeNil(), "expected PVC %s to be deleted or marked for deletion", name)
		return
	}
	Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "unexpected error getting PVC %s: %v", name, err)
}

var _ = Describe("Offline backend-storage (VMState) migration", func() {
	ctx := context.Background()

	AfterEach(func() {
		CleanupResources(ctx, k8sClient)
	})

	Describe("offlineBackendStorageCloneName", func() {
		It("builds a deterministic name from the plan suffix", func() {
			t := backendTask(backendPlanVM())
			name, err := t.offlineBackendStorageCloneName(testVM)
			Expect(err).NotTo(HaveOccurred())
			Expect(name).To(Equal(cloneBackendPVCName))
		})

		It("errors when the plan suffix is not set", func() {
			t := backendTask(backendPlanVM())
			t.Plan.Status.Suffix = nil
			_, err := t.offlineBackendStorageCloneName(testVM)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("offlineBackendStorageTarget", func() {
		It("returns the source PVC and target SC when migration is needed", func() {
			Expect(k8sClient.Create(ctx, backendStoragePVC(origBackendPVCName, backendSourceSC))).To(Succeed())
			vm := backendStorageVM()
			planVM := backendPlanVM()
			t := backendTask(planVM)
			src, sc, ok, err := t.offlineBackendStorageTarget(ctx, vm, &planVM)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(sc).To(Equal(backendTargetSC))
			Expect(src.Name).To(Equal(origBackendPVCName))
		})

		It("returns ok=false when target StorageClasses are ambiguous", func() {
			Expect(k8sClient.Create(ctx, backendStoragePVC(origBackendPVCName, backendSourceSC))).To(Succeed())
			vm := backendStorageVM()
			planVM := backendPlanVM()
			planVM.TargetMigrationPVCs = append(planVM.TargetMigrationPVCs, migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
				VolumeName: "second", DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{Name: ptr.To("second-target"), StorageClassName: ptr.To("another-sc")},
			})
			t := backendTask(planVM)
			_, _, ok, err := t.offlineBackendStorageTarget(ctx, vm, &planVM)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})

		It("returns ok=false when the VMState PVC is already on the target SC", func() {
			Expect(k8sClient.Create(ctx, backendStoragePVC(origBackendPVCName, backendTargetSC))).To(Succeed())
			vm := backendStorageVM()
			planVM := backendPlanVM()
			t := backendTask(planVM)
			_, _, ok, err := t.offlineBackendStorageTarget(ctx, vm, &planVM)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})

		It("returns ok=false when no VMState PVC exists yet", func() {
			vm := backendStorageVM()
			planVM := backendPlanVM()
			t := backendTask(planVM)
			_, _, ok, err := t.offlineBackendStorageTarget(ctx, vm, &planVM)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})
	})

	Describe("startOfflineBackendStorageMigration", func() {
		It("creates an unlabelled clone DataVolume on the target StorageClass (idempotent)", func() {
			Expect(k8sClient.Create(ctx, backendStoragePVC(origBackendPVCName, backendSourceSC))).To(Succeed())
			vm := backendStorageVM()
			planVM := backendPlanVM()
			t := backendTask(planVM)

			Expect(t.startOfflineBackendStorageMigration(ctx, vm, &planVM)).To(Succeed())

			dv := &cdiv1.DataVolume{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: cloneBackendPVCName}, dv)).To(Succeed())
			Expect(dv.Spec.Source).NotTo(BeNil())
			Expect(dv.Spec.Source.PVC).NotTo(BeNil())
			Expect(dv.Spec.Source.PVC.Name).To(Equal(origBackendPVCName))
			Expect(dv.Spec.Storage.StorageClassName).To(Equal(ptr.To(backendTargetSC)))
			Expect(dv.Labels).NotTo(HaveKey(componenthelpers.BackendStoragePVCLabel))

			// Idempotent: calling again does not error and does not create a duplicate.
			Expect(t.startOfflineBackendStorageMigration(ctx, vm, &planVM)).To(Succeed())
		})
	})

	Describe("handoffBackendStorageLabel", func() {
		It("moves the backend label from the original PVC to the clone and re-owns the clone to the VM", func() {
			vm := backendStorageVM()
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testVM}, vm)).To(Succeed())

			old := backendStoragePVC(origBackendPVCName, backendSourceSC)
			Expect(k8sClient.Create(ctx, old)).To(Succeed())
			cloneName := cloneBackendPVCName
			clone := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: cloneName, Namespace: testNamespace},
				Spec:       old.Spec,
			}
			clone.Spec.StorageClassName = ptr.To(backendTargetSC)
			Expect(k8sClient.Create(ctx, clone)).To(Succeed())
			cloneDV := &cdiv1.DataVolume{ObjectMeta: metav1.ObjectMeta{Name: cloneName, Namespace: testNamespace},
				Spec: cdiv1.DataVolumeSpec{Source: &cdiv1.DataVolumeSource{Blank: &cdiv1.DataVolumeBlankImage{}},
					Storage: &cdiv1.StorageSpec{Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Mi")}}}}}
			Expect(k8sClient.Create(ctx, cloneDV)).To(Succeed())

			t := backendTask(backendPlanVM())
			Expect(t.handoffBackendStorageLabel(ctx, vm, clone)).To(Succeed())

			// Old PVC: backend label gone, former marker added.
			gotOld := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: origBackendPVCName}, gotOld)).To(Succeed())
			Expect(gotOld.Labels).NotTo(HaveKey(componenthelpers.BackendStoragePVCLabel))
			Expect(gotOld.Labels).To(HaveKeyWithValue(formerBackendStoragePVCLabel, testVM))

			// Clone PVC: backend label present, owned by the VM.
			gotClone := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: cloneName}, gotClone)).To(Succeed())
			Expect(gotClone.Labels).To(HaveKeyWithValue(componenthelpers.BackendStoragePVCLabel, testVM))
			Expect(gotClone.OwnerReferences).To(HaveLen(1))
			Expect(gotClone.OwnerReferences[0].Kind).To(Equal("VirtualMachine"))
			Expect(gotClone.OwnerReferences[0].UID).To(Equal(vm.UID))

			// Clone DataVolume deleted.
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: cloneName}, &cdiv1.DataVolume{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("reconcileOfflineBackendStorageMigration", func() {
		It("returns false while the clone DataVolume is not yet populated", func() {
			vm := backendStorageVM()
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
			Expect(k8sClient.Create(ctx, backendStoragePVC(origBackendPVCName, backendSourceSC))).To(Succeed())
			cloneName := cloneBackendPVCName
			Expect(k8sClient.Create(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: cloneName, Namespace: testNamespace}, Spec: backendStoragePVC(cloneName, backendTargetSC).Spec})).To(Succeed())
			cloneDV := &cdiv1.DataVolume{ObjectMeta: metav1.ObjectMeta{Name: cloneName, Namespace: testNamespace},
				Spec: cdiv1.DataVolumeSpec{Source: &cdiv1.DataVolumeSource{Blank: &cdiv1.DataVolumeBlankImage{}},
					Storage: &cdiv1.StorageSpec{Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Mi")}}}},
				Status: cdiv1.DataVolumeStatus{Phase: cdiv1.Pending}}
			Expect(k8sClient.Create(ctx, cloneDV)).To(Succeed())
			// The clone PVC we created above is unlabelled, so GetBackendStoragePVC still finds the original.

			t := backendTask(backendPlanVM())
			done, err := t.reconcileOfflineBackendStorageMigration(ctx, testVM)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
		})

		It("performs the handoff and returns true when the clone is Succeeded", func() {
			vm := backendStorageVM()
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: testVM}, vm)).To(Succeed())
			Expect(k8sClient.Create(ctx, backendStoragePVC(origBackendPVCName, backendSourceSC))).To(Succeed())
			cloneName := cloneBackendPVCName
			Expect(k8sClient.Create(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: cloneName, Namespace: testNamespace}, Spec: backendStoragePVC(cloneName, backendTargetSC).Spec})).To(Succeed())
			cloneDV := &cdiv1.DataVolume{ObjectMeta: metav1.ObjectMeta{Name: cloneName, Namespace: testNamespace},
				Spec: cdiv1.DataVolumeSpec{Source: &cdiv1.DataVolumeSource{Blank: &cdiv1.DataVolumeBlankImage{}},
					Storage: &cdiv1.StorageSpec{Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Mi")}}}},
				Status: cdiv1.DataVolumeStatus{Phase: cdiv1.Pending}}
			Expect(k8sClient.Create(ctx, cloneDV)).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: cloneName}, cloneDV)).To(Succeed())
			cloneDV.Status.Phase = cdiv1.Succeeded
			Expect(k8sClient.Status().Update(ctx, cloneDV)).To(Succeed())

			t := backendTask(backendPlanVM())
			done, err := t.reconcileOfflineBackendStorageMigration(ctx, testVM)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())

			gotClone := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: cloneName}, gotClone)).To(Succeed())
			Expect(gotClone.Labels).To(HaveKeyWithValue(componenthelpers.BackendStoragePVCLabel, testVM))
		})

		It("skips the handoff when the VM has started (VMI exists)", func() {
			vm := backendStorageVM()
			Expect(k8sClient.Create(ctx, vm)).To(Succeed())
			Expect(k8sClient.Create(ctx, backendStoragePVC(origBackendPVCName, backendSourceSC))).To(Succeed())
			vmi := &virtv1.VirtualMachineInstance{ObjectMeta: metav1.ObjectMeta{Name: testVM, Namespace: testNamespace}}
			Expect(k8sClient.Create(ctx, vmi)).To(Succeed())

			t := backendTask(backendPlanVM())
			done, err := t.reconcileOfflineBackendStorageMigration(ctx, testVM)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())

			// Original PVC still carries the backend label (no handoff happened).
			gotOld := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: origBackendPVCName}, gotOld)).To(Succeed())
			Expect(gotOld.Labels).To(HaveKeyWithValue(componenthelpers.BackendStoragePVCLabel, testVM))
		})
	})

	Describe("cancelOfflineBackendStorageMigration", func() {
		It("restores the backend label on the original PVC and deletes the clone", func() {
			// Simulate a completed handoff: original stripped+marked, clone labelled.
			former := backendStoragePVC(origBackendPVCName, backendSourceSC)
			former.Labels = map[string]string{formerBackendStoragePVCLabel: testVM}
			Expect(k8sClient.Create(ctx, former)).To(Succeed())
			cloneName := cloneBackendPVCName
			clone := backendStoragePVC(cloneName, backendTargetSC)
			Expect(k8sClient.Create(ctx, clone)).To(Succeed())
			Expect(k8sClient.Create(ctx, &cdiv1.DataVolume{ObjectMeta: metav1.ObjectMeta{Name: cloneName, Namespace: testNamespace},
				Spec: cdiv1.DataVolumeSpec{Source: &cdiv1.DataVolumeSource{Blank: &cdiv1.DataVolumeBlankImage{}},
					Storage: &cdiv1.StorageSpec{Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Mi")}}}}})).To(Succeed())

			t := backendTask(backendPlanVM())
			Expect(t.cancelOfflineBackendStorageMigration(ctx, testVM)).To(Succeed())

			gotOld := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: origBackendPVCName}, gotOld)).To(Succeed())
			Expect(gotOld.Labels).To(HaveKeyWithValue(componenthelpers.BackendStoragePVCLabel, testVM))
			Expect(gotOld.Labels).NotTo(HaveKey(formerBackendStoragePVCLabel))

			expectPVCDeletedOrGone(ctx, cloneName)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: cloneName}, &cdiv1.DataVolume{})).To(HaveOccurred())
		})
	})

	Describe("deleteFormerBackendStoragePVC", func() {
		It("deletes the stripped original PVC", func() {
			former := backendStoragePVC(origBackendPVCName, backendSourceSC)
			former.Labels = map[string]string{formerBackendStoragePVCLabel: testVM}
			Expect(k8sClient.Create(ctx, former)).To(Succeed())

			t := backendTask(backendPlanVM())
			Expect(t.deleteFormerBackendStoragePVC(ctx, testVM)).To(Succeed())
			expectPVCDeletedOrGone(ctx, origBackendPVCName)
		})
	})
})
