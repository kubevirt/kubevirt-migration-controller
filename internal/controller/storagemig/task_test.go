package storagemig

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"
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
		}
	})

	AfterEach(func() {
		if controllerReconciler != nil {
			CleanupResources(ctx, controllerReconciler.Client)
			controllerReconciler = nil
		}
	})

	createValidPlanAndMigration := func(phase migrations.Phase) *migrations.VirtualMachineStorageMigration {
		migplan := testutils.NewVirtualMachineStorageMigrationPlan(testutils.TestMigPlanName)
		Expect(k8sClient.Create(ctx, migplan)).To(Succeed())
		migplan.Status.SetCondition(migrations.Condition{
			Type:     migrations.Ready,
			Status:   corev1.ConditionTrue,
			Category: migrations.Required,
			Message:  "plan is ready",
		})
		Expect(k8sClient.Status().Update(ctx, migplan)).To(Succeed())
		migration := createMigration(testutils.TestMigMigrationName)
		Expect(k8sClient.Create(ctx, migration)).To(Succeed())
		migration.Status.Phase = phase
		Expect(k8sClient.Status().Update(ctx, migration)).To(Succeed())
		return migration
	}

	Context("When the phase is Started", func() {
		It("should add finalizer to the migration when the phase is Started", func() {
			createValidPlanAndMigration(migrations.Started)
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
			createValidPlanAndMigration(migrations.RefreshStorageMigrationPlan)
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
			createValidPlanAndMigration(migrations.WaitForStorageMigrationPlanRefreshCompletion)
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
			createValidPlanAndMigration(migrations.WaitForStorageMigrationPlanRefreshCompletion)
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

	Context("when the phase is BeginLiveMigration", func() {
		BeforeEach(func() {
			createValidPlanAndMigration(migrations.BeginLiveMigration)
			createPVCs()
			createVM()
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
			createValidPlanAndMigration(migrations.WaitForLiveMigrationToComplete)
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
			createValidPlanAndMigration(migrations.CleanupMigrationResources)
			createPVCs()
			createVM()
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
	})
})
