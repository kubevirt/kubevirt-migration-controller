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

package storagemigplan

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
)

var _ = Describe("StorageMigPlan Controller envtests - with minimal real apiserver", func() {
	ctx := context.Background()

	typeNamespacedName := types.NamespacedName{
		Name:      testutils.TestMigPlanName,
		Namespace: testutils.TestNamespace,
	}

	var (
		reconciler *StorageMigPlanReconciler
		migplan    *migrations.VirtualMachineStorageMigrationPlan
	)

	BeforeEach(func() {
		reconciler = &StorageMigPlanReconciler{
			Client:        reconcilerClient,
			Scheme:        scheme.Scheme,
			EventRecorder: record.NewFakeRecorder(10),
		}
		// Create a default storage class
		storageClass := testutils.NewDefaultStorageClass("test-storage-class")
		Expect(reconciler.Client.Create(ctx, storageClass)).To(Succeed())
		migplan = testutils.NewVirtualMachineStorageMigrationPlan(testutils.TestMigPlanName, testutils.NewVirtualMachine(testutils.TestVMName, testutils.TestNamespace, testutils.TestVolumeName, testutils.TestSourcePVCName))
		Expect(reconciler.Client.Create(ctx, migplan)).To(Succeed())
	})

	AfterEach(func() {
		if reconciler != nil {
			close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
			testutils.CleanupResources(ctx, reconciler.Client)
			reconciler = nil
		}
	})

	Context("When reconciling a migplan", func() {
		It("should properly handle refresh annotation", func() {
			By("Setting the refresh annotation")
			updated := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Annotations[RefreshStartTimeAnnotation]).To(BeEmpty())
			updated.Annotations = map[string]string{
				RefreshStartTimeAnnotation: time.Now().Add(-time.Minute).Format(time.RFC3339Nano),
			}
			Expect(k8sClient.Update(ctx, updated)).To(Succeed())
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			updated = &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Annotations[RefreshStartTimeAnnotation]).ToNot(BeEmpty())
			Expect(updated.Annotations[RefreshEndTimeAnnotation]).NotTo(BeEmpty())
		})

		It("should skip reconcile if plan cannot be found", func() {
			By("Deleting the plan")
			Expect(k8sClient.Delete(ctx, migplan)).To(Succeed())
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should block deletion while active migrations exist, and allow deletion once migrations complete", func() {
			By("Reconciling to add finalizer and sync plan status")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			updated := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Finalizers).To(ContainElement(migrations.VirtualMachineStorageMigrationPlanFinalizer))

			By("Creating a migration pointing to the plan")
			migration := &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-active-migration",
					Namespace: testutils.TestNamespace,
				},
				Spec: migrations.VirtualMachineStorageMigrationSpec{
					VirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
						Name: testutils.TestMigPlanName,
						UID:  migplan.UID,
					},
				},
			}
			Expect(k8sClient.Create(ctx, migration)).To(Succeed())

			By("Setting migration phase to Started via status subresource")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-active-migration", Namespace: testutils.TestNamespace}, migration)).To(Succeed())
			migration.Status.Phase = migrations.Started
			Expect(k8sClient.Status().Update(ctx, migration)).To(Succeed())

			By("Deleting the plan (sets DeletionTimestamp, finalizer prevents hard delete)")
			Expect(k8sClient.Get(ctx, typeNamespacedName, migplan)).To(Succeed())
			Expect(k8sClient.Delete(ctx, migplan)).To(Succeed())

			By("Reconciling - deletion should be blocked by active migration")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the plan still exists with DeletionTimestamp and DeletionBlocked condition")
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.DeletionTimestamp).ToNot(BeNil())
			Expect(updated.Finalizers).To(ContainElement(migrations.VirtualMachineStorageMigrationPlanFinalizer))
			Expect(updated.Status.HasCondition(migrations.DeletionBlocked)).To(BeTrue())

			By("Completing the migration")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test-active-migration", Namespace: testutils.TestNamespace}, migration)).To(Succeed())
			migration.Status.Phase = migrations.Completed
			Expect(k8sClient.Status().Update(ctx, migration)).To(Succeed())

			By("Reconciling - deletion should proceed now")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the plan is hard-deleted")
			err = k8sClient.Get(ctx, typeNamespacedName, updated)
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("self-heal completedOutOf after deleteSource regression", func() {
		It("should heal CompletedMigrations when plan is in regressed 0/1 state", func() {
			By("Simulating the regressed state: completedOutOf=0/1 with empty CompletedMigrations")
			updated := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.CompletedMigrations).To(BeEmpty())
			updated.Status.CompletedOutOf = "0/1"
			Expect(reconciler.Client.Status().Update(ctx, updated)).To(Succeed())

			By("Creating KubeVirt")
			kv := &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{},
						VMRolloutStrategy:      ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
					},
				},
				Status: virtv1.KubeVirtStatus{OperatorVersion: "v1.5.0"},
			}
			Expect(createKubeVirt(ctx, reconciler.Client, kv)).ToNot(BeNil())

			By("Creating VM (no source PVC — already deleted by deleteSource)")
			vm := testutils.NewVirtualMachine(testutils.TestVMName, testutils.TestNamespace, testutils.TestVolumeName, testutils.TestSourcePVCName)
			Expect(reconciler.Client.Create(ctx, vm)).To(Succeed())

			By("Creating completed child migration")
			migration := &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testutils.TestMigPlanName + "-mig",
					Namespace: testutils.TestNamespace,
				},
				Spec: migrations.VirtualMachineStorageMigrationSpec{
					VirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
						Name:      testutils.TestMigPlanName,
						Namespace: testutils.TestNamespace,
					},
				},
			}
			Expect(reconciler.Client.Create(ctx, migration)).To(Succeed())
			migration.Status.Phase = migrations.Completed
			migration.Status.CompletedMigrations = []string{testutils.TestVMName}
			Expect(reconciler.Client.Status().Update(ctx, migration)).To(Succeed())

			By("reconcile heals CompletedMigrations")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.CompletedMigrations).To(HaveLen(1))
			Expect(updated.Status.CompletedMigrations[0].Name).To(Equal(testutils.TestVMName))
		})
	})

	DescribeTable("updateReadyCompletedMigrations",
		func(
			readyMigrations []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine,
			inProgressMigrations []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine,
			existingCompletedMigrations []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine,
			completedVMNames []string,
			expectedReadyCount int,
			expectedInProgressCount int,
			expectedCompletedCount int,
			expectedCompletedNames []string,
		) {
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					ReadyMigrations:      readyMigrations,
					InProgressMigrations: inProgressMigrations,
					CompletedMigrations:  existingCompletedMigrations,
				},
			}

			lastMigration := migrations.VirtualMachineStorageMigration{
				Status: migrations.VirtualMachineStorageMigrationStatus{
					CompletedMigrations: completedVMNames,
				},
			}

			err := reconciler.updateReadyCompletedMigrations(plan, lastMigration)
			Expect(err).NotTo(HaveOccurred())

			Expect(plan.Status.ReadyMigrations).To(HaveLen(expectedReadyCount))
			Expect(plan.Status.InProgressMigrations).To(HaveLen(expectedInProgressCount))
			Expect(plan.Status.CompletedMigrations).To(HaveLen(expectedCompletedCount))

			if len(expectedCompletedNames) > 0 {
				completedNames := make([]string, len(plan.Status.CompletedMigrations))
				for i, vm := range plan.Status.CompletedMigrations {
					completedNames[i] = vm.Name
					// Verify SourcePVCs are preserved when VMs move to CompletedMigrations
					if vm.Name == "vm-multidisk" {
						Expect(vm.SourcePVCs).To(HaveLen(2))
					}
				}
				for _, expectedName := range expectedCompletedNames {
					Expect(completedNames).To(ContainElement(expectedName))
				}
			}
		},
		Entry("moves completed VMs from ReadyMigrations to CompletedMigrations",
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-1"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-1", Namespace: testutils.TestNamespace},
					},
				},
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-2"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-2", Namespace: testutils.TestNamespace},
					},
				},
			},
			nil, // no inProgress
			nil, // no existing completed
			[]string{"vm-1"},
			1, // expectedReadyCount
			0, // expectedInProgressCount
			1, // expectedCompletedCount
			[]string{"vm-1"},
		),
		Entry("moves completed VMs from InProgressMigrations to CompletedMigrations",
			nil, // no ready
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-active"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-active", Namespace: testutils.TestNamespace},
					},
				},
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-running"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-running", Namespace: testutils.TestNamespace},
					},
				},
			},
			nil, // no existing completed
			[]string{"vm-active"},
			0, // expectedReadyCount
			1, // expectedInProgressCount
			1, // expectedCompletedCount
			[]string{"vm-active"},
		),
		Entry("moves completed VMs from both ReadyMigrations and InProgressMigrations",
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-ready-1"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-ready-1", Namespace: testutils.TestNamespace},
					},
				},
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-ready-2"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-ready-2", Namespace: testutils.TestNamespace},
					},
				},
			},
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-active-1"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-active-1", Namespace: testutils.TestNamespace},
					},
				},
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-active-2"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-active-2", Namespace: testutils.TestNamespace},
					},
				},
			},
			nil, // no existing completed
			[]string{"vm-ready-1", "vm-active-2"},
			1, // expectedReadyCount
			1, // expectedInProgressCount
			2, // expectedCompletedCount
			[]string{"vm-ready-1", "vm-active-2"},
		),
		Entry("preserves existing CompletedMigrations when adding new ones",
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-new"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-new", Namespace: testutils.TestNamespace},
					},
				},
			},
			nil, // no inProgress
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-old"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-old", Namespace: testutils.TestNamespace},
					},
				},
			},
			[]string{"vm-new"},
			0, // expectedReadyCount
			0, // expectedInProgressCount
			2, // expectedCompletedCount
			[]string{"vm-old", "vm-new"},
		),
		Entry("handles empty CompletedMigrations in lastMigration",
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-1"}},
			},
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-2"}},
			},
			nil, // no existing completed
			[]string{},
			1, // expectedReadyCount
			1, // expectedInProgressCount
			0, // expectedCompletedCount
			[]string{},
		),
		Entry("preserves SourcePVCs when moving VMs to CompletedMigrations",
			nil, // no ready
			[]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-multidisk"},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{Name: "pvc-boot", Namespace: testutils.TestNamespace, VolumeName: "rootdisk"},
						{Name: "pvc-data", Namespace: testutils.TestNamespace, VolumeName: "datadisk"},
					},
				},
			},
			nil, // no existing completed
			[]string{"vm-multidisk"},
			0, // expectedReadyCount
			0, // expectedInProgressCount
			1, // expectedCompletedCount
			[]string{"vm-multidisk"},
		),
	)

	DescribeTable("planCompletedByStatus",
		func(
			specVMCount int,
			completedCount int,
			expected bool,
		) {
			specVMs := make([]migrations.VirtualMachineStorageMigrationPlanVirtualMachine, specVMCount)
			for i := range specVMs {
				specVMs[i].Name = fmt.Sprintf("vm-%d", i)
			}
			completed := make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, completedCount)
			for i := range completed {
				completed[i] = completedVMStatus(fmt.Sprintf("vm-%d", i))
			}
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
					VirtualMachines: specVMs,
				},
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					CompletedMigrations: completed,
				},
			}
			Expect(planCompletedByStatus(plan)).To(Equal(expected))
		},
		Entry("false when spec has no VMs", 0, 0, false),
		Entry("false when completed migrations do not cover all VMs", 2, 1, false),
		Entry("true when every VM is completed", 1, 1, true),
	)
})

func completedVMStatus(name string) migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine {
	return migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
		VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
			Name: name,
			TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
				{
					VolumeName: testutils.TestVolumeName,
					DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
						StorageClassName: ptr.To("test-storage-class"),
					},
				},
			},
		},
		SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
			{Name: name + "-source", Namespace: testutils.TestNamespace, VolumeName: testutils.TestVolumeName},
		},
	}
}
