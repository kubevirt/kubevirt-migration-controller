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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	originalPVCName = "original-pvc"
	targetPVCName   = "target-pvc"
	testVMName      = "test-vm"
	testVolumeName  = "test-volume"
)

var _ = Describe("StorageMigPlan Controller tests without apiserver", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: testutils.TestNamespace,
		}

		var reconciler *StorageMigPlanReconciler

		BeforeEach(func() {
			reconciler = &StorageMigPlanReconciler{
				Client:        k8sClient,
				Scheme:        scheme.Scheme,
				EventRecorder: record.NewFakeRecorder(10),
				Log:           logf.Log,
			}
			// Create a default storage class
			storageClass := testutils.NewDefaultStorageClass("test-storage-class")
			Expect(reconciler.Client.Create(ctx, storageClass)).To(Succeed())
		})

		AfterEach(func() {
			if reconciler != nil {
				close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
				testutils.CleanupResources(ctx, reconciler.Client)
				reconciler = nil
			}
		})

		DescribeTable("validateKubeVirtInstalled sets correct conditions",
			func(kv *virtv1.KubeVirt, expectedReason string) {
				if kv != nil {
					createKubeVirt(ctx, reconciler.Client, kv)
					vm := testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName)
					Expect(reconciler.Client.Create(ctx, vm)).To(Succeed())
					pvc := testutils.NewPersistentVolumeClaim(originalPVCName, vm.Namespace)
					Expect(reconciler.Client.Create(ctx, pvc)).To(Succeed())
				}

				migPlan := testutils.NewVirtualMachineStorageMigrationPlan(resourceName, testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName))
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				updated := &migrations.VirtualMachineStorageMigrationPlan{}
				err = reconciler.Client.Get(ctx, typeNamespacedName, updated)
				Expect(err).NotTo(HaveOccurred())

				if expectedReason == migrations.Ready {
					Expect(updated.Status.Conditions.List).To(ContainElement(
						And(
							HaveField("Type", migrations.Ready),
							HaveField("Category", migrations.Required),
							HaveField("Status", corev1.ConditionTrue),
						),
					), "Expected conditions differ from found")
				} else {
					Expect(updated.Status.Conditions.List).To(ContainElement(
						And(
							HaveField("Type", StorageMigrationNotPossibleType),
							HaveField("Reason", expectedReason),
							HaveField("Status", corev1.ConditionTrue),
						),
					), "Expected conditions differ from found")
				}
			},
			Entry("no KubeVirt objects", nil, KubeVirtNotInstalledReason),
			Entry("invalid operator version", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "not-a-version",
				},
			}, KubeVirtVersionNotSupportedReason),
			Entry("invalid operator version, with dots", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.3.z",
				},
			}, KubeVirtVersionNotSupportedReason),
			Entry("invalid operator version, with dots", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "vx.3.0",
				},
			}, KubeVirtVersionNotSupportedReason),
			Entry("invalid operator version, with dots", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.y.0",
				},
			}, KubeVirtVersionNotSupportedReason),
			Entry("operator version < 1.3.0", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.2.0",
				},
			}, KubeVirtVersionNotSupportedReason),
			Entry("operator version >= 1.3.0 but rollout strategy not set", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{},
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.3.0",
				},
			}, KubeVirtStorageLiveMigrationNotEnabledReason),
			Entry("operator version >= 1.3.0, rollout strategy not HaveOccurredLiveUpdate", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						VMRolloutStrategy: ptr.To(virtv1.VMRolloutStrategyStage),
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.3.0",
				},
			}, KubeVirtStorageLiveMigrationNotEnabledReason),
			Entry("operator version >= 1.5.0 live migration is enabled", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{},
						VMRolloutStrategy:      ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.5.0",
				},
			}, migrations.Ready),
			Entry("operator version < 1.5.0 pre-requisites not met", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{},
						VMRolloutStrategy:      ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.4.1",
				},
			}, KubeVirtStorageLiveMigrationNotEnabledReason),
			Entry("operator version < 1.5.0 pre-requisites met", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{
							FeatureGates: []string{
								VolumesUpdateStrategy,
								VolumeMigrationConfig,
								VMLiveUpdateFeatures,
							},
						},
						VMRolloutStrategy: ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.4.1",
				},
			}, migrations.Ready),
			Entry("operator version < 1.5.0 pre-requisites met", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{
							FeatureGates: []string{
								VolumesUpdateStrategy,
								VolumeMigrationConfig,
							},
						},
						VMRolloutStrategy: ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.4.1",
				},
			}, KubeVirtStorageLiveMigrationNotEnabledReason),
			Entry("operator version < 1.5.0 pre-requisites met", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{
							FeatureGates: []string{
								VolumesUpdateStrategy,
								VMLiveUpdateFeatures,
							},
						},
						VMRolloutStrategy: ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.4.1",
				},
			}, KubeVirtStorageLiveMigrationNotEnabledReason),
			Entry("operator version < 1.5.0 pre-requisites met", &virtv1.KubeVirt{
				ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
				Spec: virtv1.KubeVirtSpec{
					Configuration: virtv1.KubeVirtConfiguration{
						DeveloperConfiguration: &virtv1.DeveloperConfiguration{
							FeatureGates: []string{
								VolumeMigrationConfig,
								VMLiveUpdateFeatures,
							},
						},
						VMRolloutStrategy: ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
					},
				},
				Status: virtv1.KubeVirtStatus{
					OperatorVersion: "v1.4.1",
				},
			}, KubeVirtStorageLiveMigrationNotEnabledReason),
		)

		Context("With valid KubeVirt object", func() {
			BeforeEach(func() {
				Expect(createKubeVirt(ctx, reconciler.Client, &virtv1.KubeVirt{
					ObjectMeta: metav1.ObjectMeta{Name: "kv", Namespace: kvNamespace},
					Spec: virtv1.KubeVirtSpec{
						Configuration: virtv1.KubeVirtConfiguration{
							DeveloperConfiguration: &virtv1.DeveloperConfiguration{},
							VMRolloutStrategy:      ptr.To(virtv1.VMRolloutStrategyLiveUpdate),
						},
					},
					Status: virtv1.KubeVirtStatus{
						OperatorVersion: "v1.5.0",
					},
				})).ToNot(BeNil())
			})

			DescribeTable("properly handles target pvc names", func(sourcePVCName, targetPVCName, expectedTargetPVCName string) {
				vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", sourcePVCName)
				Expect(reconciler.Client.Create(ctx, vm)).To(Succeed())
				sourcePVC := testutils.NewPersistentVolumeClaim(sourcePVCName, vm.Namespace)
				Expect(reconciler.Client.Create(ctx, sourcePVC)).To(Succeed())
				migPlan := testutils.NewVirtualMachineStorageMigrationPlan(resourceName, testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, sourcePVCName))
				if targetPVCName != "" {
					migPlan.Spec.VirtualMachines[0].TargetMigrationPVCs[0].DestinationPVC.Name = ptr.To(targetPVCName)
				} else {
					migPlan.Spec.VirtualMachines[0].TargetMigrationPVCs[0].DestinationPVC.Name = nil
				}
				Expect(reconciler.Client.Create(ctx, migPlan.DeepCopy())).To(Succeed())
				updated := &migrations.VirtualMachineStorageMigrationPlan{}
				Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).NotTo(HaveOccurred())
				updated.Status.Suffix = ptr.To("abcd")
				Expect(reconciler.Client.Status().Update(ctx, updated)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				updated = &migrations.VirtualMachineStorageMigrationPlan{}
				Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).NotTo(HaveOccurred())
				Expect(updated.Status.ReadyMigrations).To(HaveLen(1))
				Expect(updated.Status.ReadyMigrations[0].VirtualMachineStorageMigrationPlanVirtualMachine.TargetMigrationPVCs).To(HaveLen(1))
				Expect(updated.Status.ReadyMigrations[0].VirtualMachineStorageMigrationPlanVirtualMachine.TargetMigrationPVCs[0].DestinationPVC.Name).ToNot(BeNil())
				Expect(*updated.Status.ReadyMigrations[0].VirtualMachineStorageMigrationPlanVirtualMachine.TargetMigrationPVCs[0].DestinationPVC.Name).To(Equal(expectedTargetPVCName))

			},
				Entry("no target pvc name", originalPVCName, "", "original-pvc-mig-abcd"),
				Entry("target pvc name", originalPVCName, "test-pvc", "test-pvc"),
				Entry("source pvc with new suffix", "test-pvc-new", "", "test-pvc-mig-abcd"),
				Entry("source pvc with xyzd suffix", "test-pvc-mig-xyzd", "", "test-pvc-mig-abcd"),
				Entry("source pvc name exceeding max length", strings.Repeat("a", 60), "", strings.Repeat("a", 45)+"-92b9e111-mig-abcd"),
			)

			DescribeTable("should return an error if the target pvc is invalid", func(targetPVCDef func() *migrations.VirtualMachineStorageMigrationPlanDestinationPVC, expectType string, expectMessage string) {
				By("creating a VM and source PVC")
				vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
				Expect(reconciler.Client.Create(ctx, vm)).To(Succeed())
				sourcePVC := testutils.NewPersistentVolumeClaim(originalPVCName, vm.Namespace)
				Expect(reconciler.Client.Create(ctx, sourcePVC)).To(Succeed())
				migPlan := testutils.NewVirtualMachineStorageMigrationPlan(resourceName, testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName))
				targetPVC := targetPVCDef()
				if targetPVC != nil {
					migPlan.Spec.VirtualMachines[0].TargetMigrationPVCs[0].DestinationPVC = *targetPVC
				}
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				updated := &migrations.VirtualMachineStorageMigrationPlan{}
				Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).NotTo(HaveOccurred())
				Expect(updated.Status.Conditions.List).To(ContainElement(
					And(
						HaveField("Type", expectType),
						HaveField("Status", corev1.ConditionTrue),
						HaveField("Message", expectMessage),
					),
				), "Expected conditions differ from found")
			},
				Entry("target pvc is nil", func() *migrations.VirtualMachineStorageMigrationPlanDestinationPVC { return nil }, migrations.Ready, "plan is ready"),
				Entry("target pvc is empty", func() *migrations.VirtualMachineStorageMigrationPlanDestinationPVC {
					return &migrations.VirtualMachineStorageMigrationPlanDestinationPVC{}
				}, migrations.Ready, "plan is ready"),
				Entry("target pvc name is same as source", func() *migrations.VirtualMachineStorageMigrationPlanDestinationPVC {
					return &migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
						Name: ptr.To(originalPVCName),
					}
				}, InvalidPVCsType, "VM test-vm has a destination PVC name for volume test-volume that is the same as the source PVC name"),
				Entry("target pvc storage class is not found", func() *migrations.VirtualMachineStorageMigrationPlanDestinationPVC {
					return &migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
						StorageClassName: ptr.To("not-found"),
					}
				}, InvalidPVCsType, "storage class not-found not found"),
				Entry("target pvc storage class is not found", func() *migrations.VirtualMachineStorageMigrationPlanDestinationPVC {
					By("deleting default storage class")
					Expect(reconciler.Client.Delete(ctx, testutils.NewDefaultStorageClass("test-storage-class"))).To(Succeed())

					return &migrations.VirtualMachineStorageMigrationPlanDestinationPVC{}
				}, InvalidPVCsType, "no default storage class found"),
			)

			DescribeTable("properly set conditions based on the migration plan and status of VMs", func(vmDef func() []*virtv1.VirtualMachine, expectMessage string, expectReadyStatus corev1.ConditionStatus) {
				By("creating a VM and source PVC")
				vms := vmDef()
				for _, vm := range vms {
					Expect(reconciler.Client.Create(ctx, vm.DeepCopy())).To(Succeed())
					updated := &virtv1.VirtualMachine{}
					Expect(reconciler.Client.Get(ctx, types.NamespacedName{Namespace: vm.Namespace, Name: vm.Name}, updated)).To(Succeed())
				}
				sourcePVC := testutils.NewPersistentVolumeClaim(originalPVCName, vms[0].Namespace)
				Expect(reconciler.Client.Create(ctx, sourcePVC)).To(Succeed())
				migPlan := testutils.NewVirtualMachineStorageMigrationPlan(resourceName, vms...)
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				updated := &migrations.VirtualMachineStorageMigrationPlan{}
				readyMessage := "plan is ready"
				if expectReadyStatus == corev1.ConditionFalse {
					readyMessage = "plan has one or more critical conditions"
				}
				Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).NotTo(HaveOccurred())
				Expect(updated.Status.Conditions.List).To(ContainElements(
					And(
						HaveField("Type", migrations.Ready),
						HaveField("Status", expectReadyStatus),
						HaveField("Message", readyMessage),
					),
					And(
						HaveField("Type", NotAllVirtualMachinesReadyReason),
						HaveField("Status", corev1.ConditionTrue),
						HaveField("Message", expectMessage),
					),
				), "Expected conditions differ from found")
			},
				Entry("one vm is not ready", func() []*virtv1.VirtualMachine {
					vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
					vm.Status.Ready = false
					vm.Status.Conditions = []virtv1.VirtualMachineCondition{}
					return []*virtv1.VirtualMachine{vm}
				}, NoVirtualMachinesReadyMessage, corev1.ConditionFalse),
				Entry("one vm is not ready, one is ready", func() []*virtv1.VirtualMachine {
					vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
					vm.Status.Ready = false
					vm.Status.Conditions = []virtv1.VirtualMachineCondition{}
					vm2 := testutils.NewVirtualMachine("test-vm2", testutils.TestNamespace, "test-volume2", originalPVCName)
					vm2.Status.Ready = true
					vm2.Status.Conditions = []virtv1.VirtualMachineCondition{}
					return []*virtv1.VirtualMachine{vm, vm2}
				}, NotAllVirtualMachinesReadyMessage, corev1.ConditionTrue),
				Entry("one vm has storage live migratable condition set to false", func() []*virtv1.VirtualMachine {
					vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
					vm.Status.Conditions = []virtv1.VirtualMachineCondition{
						{
							Type:    componenthelpers.StorageLiveMigratable,
							Status:  corev1.ConditionFalse,
							Message: "storage live migration is not possible",
							Reason:  "explicitly set to false",
						},
					}
					return []*virtv1.VirtualMachine{vm}
				}, NoVirtualMachinesReadyMessage, corev1.ConditionFalse),
				Entry("one vm has storage live migratable condition set to false, one is ready", func() []*virtv1.VirtualMachine {
					vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
					vm.Status.Conditions = []virtv1.VirtualMachineCondition{
						{
							Type:    componenthelpers.StorageLiveMigratable,
							Status:  corev1.ConditionFalse,
							Message: "storage live migration is not possible",
							Reason:  "explicitly set to false",
						},
					}
					vm2 := testutils.NewVirtualMachine("test-vm2", testutils.TestNamespace, "test-volume2", originalPVCName)
					vm2.Status.Ready = true
					vm2.Status.Conditions = []virtv1.VirtualMachineCondition{}
					return []*virtv1.VirtualMachine{vm, vm2}
				}, NotAllVirtualMachinesReadyMessage, corev1.ConditionTrue),
				Entry("one vm has restart required condition set to true", func() []*virtv1.VirtualMachine {
					vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
					vm.Status.Conditions = append(vm.Status.Conditions, virtv1.VirtualMachineCondition{
						Type:    virtv1.VirtualMachineRestartRequired,
						Status:  corev1.ConditionTrue,
						Message: "virtual machine restart required",
						Reason:  "restart required",
					})
					return []*virtv1.VirtualMachine{vm}
				}, NoVirtualMachinesReadyMessage, corev1.ConditionFalse),
				Entry("one vm has restart required condition set to true, one is ready", func() []*virtv1.VirtualMachine {
					vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
					vm.Status.Conditions = append(vm.Status.Conditions, virtv1.VirtualMachineCondition{
						Type:    virtv1.VirtualMachineRestartRequired,
						Status:  corev1.ConditionTrue,
						Message: "virtual machine restart required",
						Reason:  "restart required",
					})
					vm2 := testutils.NewVirtualMachine("test-vm2", testutils.TestNamespace, "test-volume2", originalPVCName)
					vm2.Status.Ready = true
					vm2.Status.Conditions = []virtv1.VirtualMachineCondition{}
					return []*virtv1.VirtualMachine{vm, vm2}
				}, NotAllVirtualMachinesReadyMessage, corev1.ConditionTrue),
				Entry("one vm has restart required condition set to true, one has livemigratable false", func() []*virtv1.VirtualMachine {
					vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
					vm.Status.Conditions = append(vm.Status.Conditions, virtv1.VirtualMachineCondition{
						Type:    virtv1.VirtualMachineRestartRequired,
						Status:  corev1.ConditionTrue,
						Message: "virtual machine restart required",
						Reason:  "restart required",
					})
					vm2 := testutils.NewVirtualMachine("test-vm2", testutils.TestNamespace, "test-volume", originalPVCName)
					vm2.Status.Conditions = []virtv1.VirtualMachineCondition{
						{
							Type:    componenthelpers.StorageLiveMigratable,
							Status:  corev1.ConditionFalse,
							Message: "storage live migration is not possible",
							Reason:  "explicitly set to false",
						},
					}
					return []*virtv1.VirtualMachine{vm, vm2}
				}, NoVirtualMachinesReadyMessage, corev1.ConditionFalse),
			)

			It("should prefer the virt default storage class over the default storage class", func() {
				By("creating a VM and source PVC")
				vm := testutils.NewVirtualMachine("test-vm", testutils.TestNamespace, "test-volume", originalPVCName)
				Expect(reconciler.Client.Create(ctx, vm)).To(Succeed())
				sourcePVC := testutils.NewPersistentVolumeClaim(originalPVCName, vm.Namespace)
				Expect(reconciler.Client.Create(ctx, sourcePVC)).To(Succeed())
				By("creating a virt default storage class")
				virtDefaultStorageClass := testutils.NewVirtDefaultStorageClass("virt-default-storage-class")
				Expect(reconciler.Client.Create(ctx, virtDefaultStorageClass)).To(Succeed())
				migPlan := testutils.NewVirtualMachineStorageMigrationPlan(resourceName, vm)
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				updated := &migrations.VirtualMachineStorageMigrationPlan{}
				Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).To(Succeed())
				Expect(updated.Status.ReadyMigrations).To(HaveLen(1))
				Expect(updated.Status.ReadyMigrations[0].VirtualMachineStorageMigrationPlanVirtualMachine.TargetMigrationPVCs[0].DestinationPVC.StorageClassName).ToNot(BeNil())
				Expect(*updated.Status.ReadyMigrations[0].VirtualMachineStorageMigrationPlanVirtualMachine.TargetMigrationPVCs[0].DestinationPVC.StorageClassName).To(Equal("virt-default-storage-class"))
			})

			It("sets warning condition when filesystem PVC lacks cdi.kubevirt.io/storage.contentType: kubevirt annotation", func() {
				By("creating a VM and a filesystem PVC without the annotation")
				vm := testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName)
				Expect(reconciler.Client.Create(ctx, vm)).To(Succeed())
				pvc := testutils.NewPersistentVolumeClaim(originalPVCName, vm.Namespace)
				Expect(reconciler.Client.Create(ctx, pvc)).To(Succeed())
				migPlan := testutils.NewVirtualMachineStorageMigrationPlan(resourceName, testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName))
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())

				updated := &migrations.VirtualMachineStorageMigrationPlan{}
				Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).To(Succeed())
				Expect(updated.Status.ReadyMigrations).To(HaveLen(1), "plan should remain ready")
				Expect(updated.Status.Conditions.List).To(ContainElement(
					And(
						HaveField("Type", FilesystemPVCsWithoutKubeVirtContentTypeType),
						HaveField("Category", migrations.Warn),
						HaveField("Status", corev1.ConditionTrue),
						HaveField("Message", ContainSubstring("cdi.kubevirt.io/storage.contentType: kubevirt")),
					),
				), "expected warning condition for filesystem PVC without annotation")
			})

			It("does not set warning condition when filesystem PVC has di.kubevirt.io/storage.contentType: kubevirt annotation", func() {
				By("creating a VM and a filesystem PVC with the annotation")
				vm := testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName)
				Expect(reconciler.Client.Create(ctx, vm)).To(Succeed())
				pvc := testutils.NewPersistentVolumeClaim(originalPVCName, vm.Namespace)
				pvc.Annotations = map[string]string{StorageContentTypeAnnotation: StorageContentTypeKubeVirt}
				Expect(reconciler.Client.Create(ctx, pvc)).To(Succeed())
				migPlan := testutils.NewVirtualMachineStorageMigrationPlan(resourceName, testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName))
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())

				updated := &migrations.VirtualMachineStorageMigrationPlan{}
				Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).To(Succeed())
				Expect(updated.Status.ReadyMigrations).To(HaveLen(1))
				Expect(updated.Status.Conditions.List).NotTo(ContainElement(HaveField("Type", FilesystemPVCsWithoutKubeVirtContentTypeType)))
			})

			It("does not set warning condition when PVC has block volume mode", func() {
				By("creating a VM and a block PVC without the annotation")
				vm := testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName)
				Expect(reconciler.Client.Create(ctx, vm)).To(Succeed())
				pvc := testutils.NewPersistentVolumeClaim(originalPVCName, vm.Namespace)
				pvc.Spec.VolumeMode = ptr.To(corev1.PersistentVolumeBlock)
				Expect(reconciler.Client.Create(ctx, pvc)).To(Succeed())
				migPlan := testutils.NewVirtualMachineStorageMigrationPlan(resourceName, testutils.NewVirtualMachine(testVMName, testutils.TestNamespace, testVolumeName, originalPVCName))
				Expect(reconciler.Client.Create(ctx, migPlan)).To(Succeed())

				_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())

				updated := &migrations.VirtualMachineStorageMigrationPlan{}
				Expect(reconciler.Client.Get(ctx, typeNamespacedName, updated)).To(Succeed())
				Expect(updated.Status.ReadyMigrations).To(HaveLen(1))
				Expect(updated.Status.Conditions.List).NotTo(ContainElement(HaveField("Type", FilesystemPVCsWithoutKubeVirtContentTypeType)))
			})
		})
	})
})

func createKubeVirt(ctx context.Context, client client.Client, kv *virtv1.KubeVirt) *virtv1.KubeVirt {
	Expect(client.Create(ctx, kv)).To(Succeed())
	createdKv := &virtv1.KubeVirt{}
	Expect(client.Get(ctx, types.NamespacedName{Name: kv.Name, Namespace: kv.Namespace}, createdKv)).To(Succeed())
	return createdKv
}
