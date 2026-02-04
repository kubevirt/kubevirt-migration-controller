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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	"kubevirt.io/kubevirt-migration-controller/internal/controller/storagemigplan"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
)

var _ = Describe("StorageMigration tasks", func() {
	var (
		controllerReconciler *StorageMigrationReconciler
	)
	ctx := context.Background()

	typeNamespacedName := types.NamespacedName{
		Name:      testutils.TestMigMigrationName,
		Namespace: testutils.TestNamespace,
	}

	BeforeEach(func() {
		controllerReconciler = &StorageMigrationReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			Config: cfg,
			Log:    logf.Log.WithName("storagemig-controller"),
		}
	})

	AfterEach(func() {
		if controllerReconciler != nil {
			CleanupResources(ctx, controllerReconciler.Client)
			controllerReconciler = nil
		}
	})

	createValidPlanAndMigration := func(phase migrations.Phase, retentionPolicy *migrations.RetentionPolicy) *migrations.VirtualMachineStorageMigration {
		migplan := testutils.NewVirtualMachineStorageMigrationPlan(testutils.TestMigPlanName, testutils.NewVirtualMachine(testutils.TestVMName, testutils.TestNamespace, testutils.TestVolumeName, testutils.TestSourcePVCName))
		if retentionPolicy != nil {
			migplan.Spec.RetentionPolicy = retentionPolicy
		}
		Expect(k8sClient.Create(ctx, migplan)).To(Succeed())
		migplan.Status.SetCondition(migrations.Condition{
			Type:     migrations.Ready,
			Status:   corev1.ConditionTrue,
			Category: migrations.Required,
			Message:  "plan is ready",
		})
		Expect(k8sClient.Status().Update(ctx, migplan)).To(Succeed())
		migration := createMigration()
		Expect(k8sClient.Create(ctx, migration)).To(Succeed())
		migration.Status.Phase = phase
		Expect(k8sClient.Status().Update(ctx, migration)).To(Succeed())
		return migration
	}

	Context("When the phase is Started", func() {
		It("should add finalizer to the migration when the phase is Started", func() {
			createValidPlanAndMigration(migrations.Started, nil)
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.RefreshStorageMigrationPlan))
			Expect(migration.Finalizers).To(ContainElement(migrations.VirtualMachineStorageMigrationFinalizer))
		})
	})

	Context("When the phase is RefreshReadyVirtualMachines", func() {
		It("should refresh ready virtual machines when the phase is RefreshReadyVirtualMachines", func() {
			createValidPlanAndMigration(migrations.RefreshStorageMigrationPlan, nil)
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.WaitForStorageMigrationPlanRefreshCompletion))
			By("checking the plan has the appropriate annotations")
			plan := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestMigPlanName, Namespace: testutils.TestNamespace}, plan)).To(Succeed())
			Expect(plan.Annotations[storagemigplan.RefreshStartTimeAnnotation]).ToNot(BeEmpty())
		})
	})

	Context("When the phase is RefreshCompletedVirtualMachines", func() {
		It("should start migration when the plan is refreshed", func() {
			createValidPlanAndMigration(migrations.WaitForStorageMigrationPlanRefreshCompletion, ptr.To(migrations.RetentionPolicyKeepSource))
			plan := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestMigPlanName, Namespace: testutils.TestNamespace}, plan)).To(Succeed())
			plan.Annotations = map[string]string{}
			plan.Annotations[storagemigplan.RefreshStartTimeAnnotation] = time.Now().Add(-time.Minute).Format(time.RFC3339Nano)
			plan.Annotations[storagemigplan.RefreshEndTimeAnnotation] = time.Now().Format(time.RFC3339Nano)
			Expect(k8sClient.Update(ctx, plan)).To(Succeed())
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.BeginLiveMigration))
		})

		DescribeTable("should not start migration when the plan is not refreshed", func(startAnnotation func() string, endAnnotation func() string, expectError bool, nextPhase migrations.Phase) {
			createValidPlanAndMigration(migrations.WaitForStorageMigrationPlanRefreshCompletion, ptr.To(migrations.RetentionPolicyDeleteSource))
			plan := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestMigPlanName, Namespace: testutils.TestNamespace}, plan)).To(Succeed())
			startAnn := startAnnotation()
			endAnn := endAnnotation()
			if startAnn != "" {
				plan.Annotations = map[string]string{}
				plan.Annotations[storagemigplan.RefreshStartTimeAnnotation] = startAnn
				plan.Annotations[storagemigplan.RefreshEndTimeAnnotation] = endAnn
			}
			Expect(k8sClient.Update(ctx, plan)).To(Succeed())
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			if expectError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(nextPhase))
		},
			Entry("no refresh start time annotation",
				func() string { return "" },
				func() string { return time.Now().Format(time.RFC3339Nano) },
				true,
				migrations.WaitForStorageMigrationPlanRefreshCompletion),
			Entry("before refresh start time",
				func() string { return time.Now().Add(-time.Minute).Format(time.RFC3339Nano) },
				func() string { return time.Now().Format(time.RFC3339Nano) },
				false,
				migrations.BeginLiveMigration),
			Entry("before refresh start time, invalid end time",
				func() string { return time.Now().Add(-time.Minute).Format(time.RFC3339Nano) },
				func() string { return "not a valid time" },
				true,
				migrations.WaitForStorageMigrationPlanRefreshCompletion),
			Entry("after refresh start time",
				func() string { return time.Now().Add(time.Minute).Format(time.RFC3339Nano) },
				func() string { return time.Now().Format(time.RFC3339Nano) },
				false,
				migrations.WaitForStorageMigrationPlanRefreshCompletion),
			Entry("refresh start time is not a valid time",
				func() string { return "not a valid time" },
				func() string { return time.Now().Format(time.RFC3339Nano) },
				true,
				migrations.WaitForStorageMigrationPlanRefreshCompletion),
		)
	})

	createReadyStorageMigrationPlanStatus := func(plan *migrations.VirtualMachineStorageMigrationPlan) *migrations.VirtualMachineStorageMigrationPlanStatus {
		return &migrations.VirtualMachineStorageMigrationPlanStatus{
			ReadyMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
						Name:                plan.Spec.VirtualMachines[0].Name,
						TargetMigrationPVCs: plan.Spec.VirtualMachines[0].TargetMigrationPVCs,
					},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{
							VolumeName: testutils.TestVolumeName,
							Name:       testutils.TestSourcePVCName,
							Namespace:  testutils.TestNamespace,
							SourcePVC:  *testutils.NewPersistentVolumeClaim(testutils.TestSourcePVCName, testutils.TestNamespace),
						},
					},
				},
			},
		}
	}
	createPVCs := func() {
		pvc := testutils.NewPersistentVolumeClaim(testutils.TestSourcePVCName, testutils.TestNamespace)
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())
		pvc = testutils.NewPersistentVolumeClaim(testutils.TestTargetPVCName, testutils.TestNamespace)
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())
	}

	createVM := func() {
		vm := testutils.NewVirtualMachine(testutils.TestVMName, testutils.TestNamespace, testutils.TestVolumeName, testutils.TestSourcePVCName)
		Expect(k8sClient.Create(ctx, vm)).To(Succeed())
	}

	createVMI := func() {
		vmi := testutils.NewVirtualMachineInstance(testutils.TestVMName, testutils.TestNamespace, types.UID("1111-1111"), testutils.TestNodeName)
		Expect(k8sClient.Create(ctx, vmi)).To(Succeed())
	}

	Context("when the phase is BeginLiveMigration", func() {
		BeforeEach(func() {
			createValidPlanAndMigration(migrations.BeginLiveMigration, ptr.To(migrations.RetentionPolicyKeepSource))
			createPVCs()
			createVM()
			createVMI()
		})

		DescribeTable("should properly handle live migration when the phase is BeginLiveMigration", func(ready bool) {
			By("marking the VM as ready in the migplan")
			plan := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestMigPlanName, Namespace: testutils.TestNamespace}, plan)).To(Succeed())
			plan.Status = *createReadyStorageMigrationPlanStatus(plan)
			Expect(k8sClient.Status().Update(ctx, plan)).To(Succeed())

			vm := &virtv1.VirtualMachine{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestVMName, Namespace: testutils.TestNamespace}, vm)).To(Succeed())
			vm.Status.Ready = ready
			Expect(k8sClient.Update(ctx, vm)).To(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(controllerReconciler.Client.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.WaitForLiveMigrationToComplete))
			if ready {
				Expect(migration.Status.RunningMigrations).To(ContainElement(migrations.RunningVirtualMachineMigration{
					Name:     testutils.TestVMName,
					Progress: "",
				}))
			} else {
				Expect(migration.Status.RunningMigrations).To(BeEmpty())
			}
			Expect(migration.Status.CompletedMigrations).To(BeEmpty())
		}, Entry("all VMs are ready to migrate", true),
			Entry("all VMs are not ready to migrate", false),
		)
	})

	Context("when the phase is WaitForLiveMigrationToComplete", func() {
		BeforeEach(func() {
			createValidPlanAndMigration(migrations.WaitForLiveMigrationToComplete, ptr.To(migrations.RetentionPolicyKeepSource))
			createPVCs()
			createVM()

			Expect(os.Setenv("CONTROLLER_NAMESPACE", testutils.TestNamespace)).To(Succeed())
		})

		AfterEach(func() {
			Expect(os.Unsetenv("CONTROLLER_NAMESPACE")).To(Succeed())
		})

		It("should properly handle in progress live migration not being completed", func() {
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(controllerReconciler.Client.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			migration.Status.RunningMigrations = []migrations.RunningVirtualMachineMigration{
				{
					Name: testutils.TestVMName,
				},
			}
			Expect(k8sClient.Status().Update(ctx, migration)).To(Succeed())
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(controllerReconciler.Client.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.WaitForLiveMigrationToComplete))
			Expect(migration.Status.RunningMigrations).To(ContainElement(migrations.RunningVirtualMachineMigration{
				Name:     testutils.TestVMName,
				Progress: "",
			}))
			Expect(migration.Status.CompletedMigrations).To(BeEmpty())

			By("creating failed VMIM")
			vmim := &virtv1.VirtualMachineInstanceMigration{}
			vmim.Name = testutils.TestVMIMName
			vmim.Namespace = testutils.TestNamespace
			vmim.Spec.VMIName = testutils.TestVMName
			vmim.Status.Phase = virtv1.MigrationFailed
			vmim.Status.MigrationState = &virtv1.VirtualMachineInstanceMigrationState{
				Completed:      true,
				Failed:         true,
				FailureReason:  "test failure",
				StartTimestamp: &metav1.Time{Time: time.Now().Add(-time.Minute)},
				EndTimestamp:   &metav1.Time{Time: time.Now()},
			}
			Expect(k8sClient.Create(ctx, vmim)).To(Succeed())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(controllerReconciler.Client.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.WaitForLiveMigrationToComplete))
			Expect(migration.Status.RunningMigrations).To(ContainElement(migrations.RunningVirtualMachineMigration{
				Name:     testutils.TestVMName,
				Progress: "",
			}))
			Expect(migration.Status.CompletedMigrations).To(BeEmpty())

			By("creating completed VMIM")
			vmim = &virtv1.VirtualMachineInstanceMigration{}
			vmim.Name = testutils.TestVMIMName + "-completed"
			vmim.Namespace = testutils.TestNamespace
			vmim.Spec.VMIName = testutils.TestVMName
			vmim.Status.Phase = virtv1.MigrationSucceeded
			vmim.Status.MigrationState = &virtv1.VirtualMachineInstanceMigrationState{
				Completed:      true,
				StartTimestamp: &metav1.Time{Time: time.Now().Add(-time.Second)},
				EndTimestamp:   &metav1.Time{Time: time.Now()},
			}
			Expect(k8sClient.Create(ctx, vmim)).To(Succeed())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration = &migrations.VirtualMachineStorageMigration{}
			Expect(controllerReconciler.Client.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.CleanupMigrationResources))
			Expect(migration.Status.RunningMigrations).To(BeEmpty())
			Expect(migration.Status.CompletedMigrations).To(ContainElement(testutils.TestVMName))
		})
	})

	Context("when the phase is CleanupMigrationResources", func() {
		BeforeEach(func() {
			createPVCs()
			createVM()
			createVMI()
		})

		completeMigration := func() {
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(controllerReconciler.Client.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			migration.Status.CompletedMigrations = []string{testutils.TestVMName}
			Expect(k8sClient.Status().Update(ctx, migration)).To(Succeed())
		}

		createPodContainer := func() []corev1.Container {
			return []corev1.Container{
				{
					Image: "virt-launcher:latest",
					Name:  "virt-launcher",
				},
			}
		}

		It("should properly cleanup migration resources", func() {
			createValidPlanAndMigration(migrations.CleanupMigrationResources, nil)
			completeMigration()
			By("creating a completed virt-launcher pod")
			pod := &corev1.Pod{}
			pod.Name = testutils.TestVMName + "-virt-launcher-completed"
			pod.Namespace = testutils.TestNamespace
			pod.Labels = map[string]string{
				virtLauncherPodLabelSelectorKey: virtLauncherPodLabelSelectorValue,
			}
			pod.Spec.Containers = createPodContainer()
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			pod.Status.Phase = corev1.PodSucceeded
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
			By("creating an unrelated pod")
			unrelatedPod := &corev1.Pod{}
			unrelatedPod.Name = testutils.TestVMName + "-unrelated"
			unrelatedPod.Namespace = testutils.TestNamespace
			unrelatedPod.Labels = map[string]string{
				"unrelated": "unrelated",
			}
			unrelatedPod.Spec.Containers = createPodContainer()
			Expect(k8sClient.Create(ctx, unrelatedPod)).To(Succeed())
			unrelatedPod.Status.Phase = corev1.PodRunning
			Expect(k8sClient.Status().Update(ctx, unrelatedPod)).To(Succeed())
			By("creating a running virt-launcher pod")
			runningPod := &corev1.Pod{}
			runningPod.Name = testutils.TestVMName + "-virt-launcher-running"
			runningPod.Namespace = testutils.TestNamespace
			runningPod.Labels = map[string]string{
				virtLauncherPodLabelSelectorKey: virtLauncherPodLabelSelectorValue,
			}
			runningPod.Spec.Containers = createPodContainer()
			Expect(k8sClient.Create(ctx, runningPod)).To(Succeed())
			runningPod.Status.Phase = corev1.PodRunning
			Expect(k8sClient.Status().Update(ctx, runningPod)).To(Succeed())
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(controllerReconciler.Client.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.Completed))
			podList := &corev1.PodList{}
			Expect(k8sClient.List(ctx, podList, k8sclient.InNamespace(testutils.TestNamespace))).To(Succeed())
			Expect(podList.Items).To(HaveLen(2))
			Expect(podList.Items).To(ContainElements(And(
				HaveField("Name", unrelatedPod.Name),
			), And(
				HaveField("Name", runningPod.Name),
			)))
		})

		It("should delete source PVC when retentionPolicy is deleteSource after migration completes", func() {
			createValidPlanAndMigration(migrations.CleanupMigrationResources, ptr.To(migrations.RetentionPolicyDeleteSource))
			plan := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestMigPlanName, Namespace: testutils.TestNamespace}, plan)).To(Succeed())

			By("setting plan status with CompletedMigrations and SourcePVCs so source can be found for deletion")
			plan = &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestMigPlanName, Namespace: testutils.TestNamespace}, plan)).To(Succeed())
			plan.Status.CompletedMigrations = []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
				{
					VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
						Name:                testutils.TestVMName,
						TargetMigrationPVCs: plan.Spec.VirtualMachines[0].TargetMigrationPVCs,
					},
					SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
						{
							VolumeName: testutils.TestVolumeName,
							Name:       testutils.TestSourcePVCName,
							Namespace:  testutils.TestNamespace,
							SourcePVC:  *testutils.NewPersistentVolumeClaim(testutils.TestSourcePVCName, testutils.TestNamespace),
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, plan)).To(Succeed())

			completeMigration()
			By("creating a completed virt-launcher pod so cleanup proceeds")
			pod := &corev1.Pod{}
			pod.Name = testutils.TestVMName + "-virt-launcher-completed"
			pod.Namespace = testutils.TestNamespace
			pod.Labels = map[string]string{
				virtLauncherPodLabelSelectorKey: virtLauncherPodLabelSelectorValue,
			}
			pod.Spec.Containers = createPodContainer()
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			pod.Status.Phase = corev1.PodSucceeded
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			migration := &migrations.VirtualMachineStorageMigration{}
			Expect(controllerReconciler.Client.Get(ctx, typeNamespacedName, migration)).To(Succeed())
			Expect(migration.Status.Phase).To(Equal(migrations.Completed))

			By("verifying source PVC was deleted due to retentionPolicy deleteSource")
			sourcePVC := &corev1.PersistentVolumeClaim{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestSourcePVCName, Namespace: testutils.TestNamespace}, sourcePVC)

			if err != nil {
				Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			} else {
				Expect(sourcePVC.DeletionTimestamp).ToNot(BeNil())
			}

			By("verifying target PVC still exists")
			targetPVC := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testutils.TestTargetPVCName, Namespace: testutils.TestNamespace}, targetPVC)).To(Succeed())
		})
	})

	Context("getSourcePVCsForCompletedMigrations", func() {
		It("returns only source PVCs from CompletedMigrations for VMs in the completed list", func() {
			sourcePVCCompleted := migrations.VirtualMachineStorageMigrationPlanSourcePVC{
				VolumeName: "vol-completed",
				Name:       "pvc-completed",
				Namespace:  testutils.TestNamespace,
				SourcePVC:  *testutils.NewPersistentVolumeClaim("pvc-completed", testutils.TestNamespace),
			}
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan", Namespace: testutils.TestNamespace},
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					ReadyMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-ready"},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: "pvc-ready", Namespace: testutils.TestNamespace},
							},
						},
					},
					InProgressMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-inprogress"},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: "pvc-inprogress", Namespace: testutils.TestNamespace},
							},
						},
					},
					CompletedMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-completed"},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{sourcePVCCompleted},
						},
					},
				},
			}
			task := &Task{
				Plan: plan,
				Log:  logf.Log.WithName("test"),
			}
			pvcs := task.getSourcePVCsForCompletedMigrations([]string{"vm-completed"})
			Expect(pvcs).To(HaveLen(1))
			Expect(pvcs[0].Name).To(Equal("pvc-completed"))
			Expect(pvcs[0].Namespace).To(Equal(testutils.TestNamespace))
		})

		It("returns empty when completedVMNames is empty", func() {
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan", Namespace: testutils.TestNamespace},
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					CompletedMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-completed"},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: "pvc-completed", Namespace: testutils.TestNamespace},
							},
						},
					},
				},
			}
			task := &Task{Plan: plan, Log: logf.Log.WithName("test")}
			pvcs := task.getSourcePVCsForCompletedMigrations(nil)
			Expect(pvcs).To(BeEmpty())
			pvcs = task.getSourcePVCsForCompletedMigrations([]string{})
			Expect(pvcs).To(BeEmpty())
		})

		It("returns empty when only ReadyMigrations and InProgressMigrations have VMs (no completed match)", func() {
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan", Namespace: testutils.TestNamespace},
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					ReadyMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-ready"},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: "pvc-ready", Namespace: testutils.TestNamespace},
							},
						},
					},
					InProgressMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-inprogress"},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: "pvc-inprogress", Namespace: testutils.TestNamespace},
							},
						},
					},
					CompletedMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{},
				},
			}
			task := &Task{Plan: plan, Log: logf.Log.WithName("test")}
			// Passing ready/inprogress VM names does not return their PVCs because only CompletedMigrations is consulted.
			pvcs := task.getSourcePVCsForCompletedMigrations([]string{"vm-ready", "vm-inprogress"})
			Expect(pvcs).To(BeEmpty())
		})

		It("returns only matching completed VMs and ignores non-matching completed entries", func() {
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan", Namespace: testutils.TestNamespace},
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					CompletedMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-a"},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: "pvc-a", Namespace: testutils.TestNamespace},
							},
						},
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: "vm-b"},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: "pvc-b", Namespace: testutils.TestNamespace},
							},
						},
					},
				},
			}
			task := &Task{Plan: plan, Log: logf.Log.WithName("test")}
			pvcs := task.getSourcePVCsForCompletedMigrations([]string{"vm-a"})
			Expect(pvcs).To(HaveLen(1))
			Expect(pvcs[0].Name).To(Equal("pvc-a"))
		})
	})

	Context("deleteSourceDataVolumesAndPVCs", func() {
		const (
			vmReadyName       = "vm-ready"
			vmInProgressName  = "vm-inprogress"
			vmCompletedName   = "vm-completed"
			pvcReadyName      = "pvc-ready"
			pvcInProgressName = "pvc-inprogress"
			pvcCompletedName  = "pvc-completed"
		)

		It("deletes only source PVCs for completed migrations; leaves ready and in-progress source PVCs untouched", func() {
			By("creating PVCs for ready, in-progress, and completed migrations")
			pvcReady := testutils.NewPersistentVolumeClaim(pvcReadyName, testutils.TestNamespace)
			Expect(k8sClient.Create(ctx, pvcReady)).To(Succeed())
			pvcInProgress := testutils.NewPersistentVolumeClaim(pvcInProgressName, testutils.TestNamespace)
			Expect(k8sClient.Create(ctx, pvcInProgress)).To(Succeed())
			pvcCompleted := testutils.NewPersistentVolumeClaim(pvcCompletedName, testutils.TestNamespace)
			Expect(k8sClient.Create(ctx, pvcCompleted)).To(Succeed())

			By("creating a plan with ReadyMigrations, InProgressMigrations, and CompletedMigrations each with different source PVCs")
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-delete-test", Namespace: testutils.TestNamespace},
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					ReadyMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: vmReadyName},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: pvcReadyName, Namespace: testutils.TestNamespace, SourcePVC: *pvcReady},
							},
						},
					},
					InProgressMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: vmInProgressName},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: pvcInProgressName, Namespace: testutils.TestNamespace, SourcePVC: *pvcInProgress},
							},
						},
					},
					CompletedMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: vmCompletedName},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: pvcCompletedName, Namespace: testutils.TestNamespace, SourcePVC: *pvcCompleted},
							},
						},
					},
				},
			}

			task := &Task{
				Plan:   plan,
				Client: controllerReconciler.Client,
				Log:    logf.Log.WithName("test"),
			}

			By("calling deleteSourceDataVolumesAndPVCs with only the completed VM name")
			Expect(task.deleteSourceDataVolumesAndPVCs(ctx, []string{vmCompletedName})).To(Succeed())

			By("verifying only the completed migration's source PVC was deleted")

			errCompleted := k8sClient.Get(ctx, types.NamespacedName{Name: pvcCompletedName, Namespace: testutils.TestNamespace}, pvcCompleted)
			if errCompleted != nil {
				Expect(k8serrors.IsNotFound(errCompleted)).To(BeTrue(), "pvc-completed should be deleted")
			} else {
				// Make sure the deletion timestamp is set on the PVC
				Expect(pvcCompleted.DeletionTimestamp).ToNot(BeNil())
			}

			By("verifying ready and in-progress source PVCs still exist")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvcReadyName, Namespace: testutils.TestNamespace}, &corev1.PersistentVolumeClaim{})).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvcInProgressName, Namespace: testutils.TestNamespace}, &corev1.PersistentVolumeClaim{})).To(Succeed())
		})

		It("deletes DataVolume for completed migration when source is a DataVolume", func() {
			By("creating a DataVolume and PVC for the completed migration (CDI uses same name for DV and PVC)")
			dvCompleted := &cdiv1.DataVolume{
				ObjectMeta: metav1.ObjectMeta{Name: pvcCompletedName, Namespace: testutils.TestNamespace},
				Spec: cdiv1.DataVolumeSpec{
					PVC: &corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("1Gi"),
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dvCompleted)).To(Succeed())
			pvcCompleted := testutils.NewPersistentVolumeClaim(pvcCompletedName, testutils.TestNamespace)
			Expect(k8sClient.Create(ctx, pvcCompleted)).To(Succeed())

			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-dv-delete-test", Namespace: testutils.TestNamespace},
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					CompletedMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: vmCompletedName},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: pvcCompletedName, Namespace: testutils.TestNamespace, SourcePVC: *pvcCompleted},
							},
						},
					},
				},
			}
			task := &Task{
				Plan:   plan,
				Client: controllerReconciler.Client,
				Log:    logf.Log.WithName("test"),
			}
			Expect(task.deleteSourceDataVolumesAndPVCs(ctx, []string{vmCompletedName})).To(Succeed())

			By("verifying the DataVolume associated with the completed migration was deleted")
			errDV := k8sClient.Get(ctx, types.NamespacedName{Name: pvcCompletedName, Namespace: testutils.TestNamespace}, &cdiv1.DataVolume{})
			Expect(k8serrors.IsNotFound(errDV)).To(BeTrue(), "DataVolume for completed migration should be deleted")
		})

		It("deletes only DataVolumes for completed migrations; does not delete DVs or PVCs for ready or in-progress", func() {
			By("creating DataVolumes and PVCs for ready, in-progress, and completed migrations")
			newDV := func(name string) *cdiv1.DataVolume {
				return &cdiv1.DataVolume{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testutils.TestNamespace},
					Spec: cdiv1.DataVolumeSpec{
						PVC: &corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceStorage: resource.MustParse("1Gi"),
								},
							},
						},
					},
				}
			}
			Expect(k8sClient.Create(ctx, newDV(pvcReadyName))).To(Succeed())
			Expect(k8sClient.Create(ctx, testutils.NewPersistentVolumeClaim(pvcReadyName, testutils.TestNamespace))).To(Succeed())
			Expect(k8sClient.Create(ctx, newDV(pvcInProgressName))).To(Succeed())
			Expect(k8sClient.Create(ctx, testutils.NewPersistentVolumeClaim(pvcInProgressName, testutils.TestNamespace))).To(Succeed())
			Expect(k8sClient.Create(ctx, newDV(pvcCompletedName))).To(Succeed())
			pvcCompleted := testutils.NewPersistentVolumeClaim(pvcCompletedName, testutils.TestNamespace)
			Expect(k8sClient.Create(ctx, pvcCompleted)).To(Succeed())

			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "plan-dv-all-test", Namespace: testutils.TestNamespace},
				Status: migrations.VirtualMachineStorageMigrationPlanStatus{
					ReadyMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: vmReadyName},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: pvcReadyName, Namespace: testutils.TestNamespace},
							},
						},
					},
					InProgressMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: vmInProgressName},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: pvcInProgressName, Namespace: testutils.TestNamespace},
							},
						},
					},
					CompletedMigrations: []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
						{
							VirtualMachineStorageMigrationPlanVirtualMachine: migrations.VirtualMachineStorageMigrationPlanVirtualMachine{Name: vmCompletedName},
							SourcePVCs: []migrations.VirtualMachineStorageMigrationPlanSourcePVC{
								{Name: pvcCompletedName, Namespace: testutils.TestNamespace, SourcePVC: *pvcCompleted},
							},
						},
					},
				},
			}
			task := &Task{
				Plan:   plan,
				Client: controllerReconciler.Client,
				Log:    logf.Log.WithName("test"),
			}
			Expect(task.deleteSourceDataVolumesAndPVCs(ctx, []string{vmCompletedName})).To(Succeed())

			By("verifying completed migration DataVolume was deleted")
			errCompletedDV := k8sClient.Get(ctx, types.NamespacedName{Name: pvcCompletedName, Namespace: testutils.TestNamespace}, &cdiv1.DataVolume{})
			Expect(k8serrors.IsNotFound(errCompletedDV)).To(BeTrue(), "DataVolume for completed migration should be deleted")

			By("verifying ready and in-progress DataVolumes still exist")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvcReadyName, Namespace: testutils.TestNamespace}, &cdiv1.DataVolume{})).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvcInProgressName, Namespace: testutils.TestNamespace}, &cdiv1.DataVolume{})).To(Succeed())

			By("verifying ready and in-progress source PVCs still exist")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvcReadyName, Namespace: testutils.TestNamespace}, &corev1.PersistentVolumeClaim{})).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvcInProgressName, Namespace: testutils.TestNamespace}, &corev1.PersistentVolumeClaim{})).To(Succeed())
		})

		It("does nothing when Plan is nil", func() {
			pvc := testutils.NewPersistentVolumeClaim("pvc-orphan", testutils.TestNamespace)
			Expect(k8sClient.Create(ctx, pvc)).To(Succeed())
			task := &Task{
				Plan:   nil,
				Client: controllerReconciler.Client,
				Log:    logf.Log.WithName("test"),
			}
			Expect(task.deleteSourceDataVolumesAndPVCs(ctx, []string{"any-vm"})).To(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "pvc-orphan", Namespace: testutils.TestNamespace}, &corev1.PersistentVolumeClaim{})).To(Succeed())
		})
	})
})
