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
package e2e

import (
	"context"
	"fmt"
	"time"

	expect "github.com/google/goexpect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	migrationsv1 "kubevirt.io/api/migrations/v1alpha1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	"kubevirt.io/kubevirt-migration-controller/test/utils/console"
	cd "kubevirt.io/kubevirt-migration-controller/test/utils/containerdisk"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libdv"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libvmi"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libvmifact"
	"kubevirt.io/kubevirt-migration-controller/test/utils/libwait"
	"kubevirt.io/kubevirt-migration-controller/test/utils/matcher"
)

// copyProxyCAHelper copies the registry proxy CA to the test namespace
func copyProxyCAHelper(namespace string) {
	By("Copying proxy CA to test namespace")
	ca := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: registryProxyCACertName, Namespace: *registryProxyNamespace},
	}
	err := c.Get(context.TODO(), client.ObjectKeyFromObject(ca), ca, &client.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	newCa := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: registryProxyCACertName, Namespace: namespace},
		Data:       ca.Data,
	}
	err = c.Create(context.TODO(), newCa, &client.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
	By("Waiting for proxy CA to be copied to test namespace")
	Eventually(func() bool {
		ca := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: registryProxyCACertName, Namespace: namespace},
		}
		err := c.Get(context.TODO(), client.ObjectKeyFromObject(ca), ca, &client.GetOptions{})
		return err == nil
	}, 10*time.Second, 1*time.Second).Should(BeTrue())
}

// setupNamespaceAndStorageClassHelper creates a test namespace and resolves the default storage class
func setupNamespaceAndStorageClassHelper(namespacePrefix string) (*corev1.Namespace, string) {
	namespaceName := namespacePrefix + rand.String(6)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
	}
	By("Creating test namespace " + ns.Name)
	err := c.Create(context.TODO(), ns, &client.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())

	By("Resolving default storage class")
	scList := &storagev1.StorageClassList{}
	err = c.List(context.TODO(), scList, &client.ListOptions{})
	Expect(err).NotTo(HaveOccurred())
	var sc string
	for i := range scList.Items {
		if scList.Items[i].Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			sc = scList.Items[i].Name
			break
		}
	}
	if sc == "" && len(scList.Items) > 0 {
		sc = scList.Items[0].Name
	}
	Expect(sc).NotTo(BeEmpty(), "cluster must have at least one storage class")
	copyProxyCAHelper(ns.Name)
	return ns, sc
}

// cleanupNamespaceHelper deletes the test namespace
func cleanupNamespaceHelper(ns *corev1.Namespace) {
	By("Deleting test namespace")
	Eventually(func() bool {
		err := c.Delete(context.TODO(), ns, &client.DeleteOptions{})
		if k8serrors.IsNotFound(err) {
			return true
		}
		Expect(err).NotTo(HaveOccurred())
		return false
	}, 60*time.Second, 2*time.Second).Should(BeTrue())
}

var _ = Describe("MigPlan", func() {
	var (
		namespace *corev1.Namespace
	)

	BeforeEach(func() {
		By("Creating a new test namespace")
		namespaceName := "e2e-test-migplan-" + rand.String(6)
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
		}
		err := c.Create(context.TODO(), namespace, &client.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		By("Deleting the test migplan")
		Eventually(func() bool {
			migrationPlan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: namespace.Name},
			}
			err := c.Delete(context.TODO(), migrationPlan, &client.DeleteOptions{})
			if k8serrors.IsNotFound(err) {
				return true
			}
			Expect(err).ToNot(HaveOccurred())
			return k8serrors.IsNotFound(err)
		}, 30*time.Second, time.Second).Should(BeTrue())

		By("Deleting the test duplicates migplan")
		Eventually(func() bool {
			migrationPlan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test-plan-duplicates", Namespace: namespace.Name},
			}
			err := c.Delete(context.TODO(), migrationPlan, &client.DeleteOptions{})
			if k8serrors.IsNotFound(err) {
				return true
			}
			Expect(err).ToNot(HaveOccurred())
			return k8serrors.IsNotFound(err)
		}, 30*time.Second, time.Second).Should(BeTrue())

		By("Deleting the test multi-namespace plan")
		Eventually(func() bool {
			multiNsPlan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test-multinamespace-plan-duplicates", Namespace: namespace.Name},
			}
			err := c.Delete(context.TODO(), multiNsPlan, &client.DeleteOptions{})
			if k8serrors.IsNotFound(err) {
				return true
			}
			Expect(err).ToNot(HaveOccurred())
			return k8serrors.IsNotFound(err)
		}, 30*time.Second, time.Second).Should(BeTrue())

		By("Deleting the test namespace")
		Eventually(func() bool {
			err := c.Delete(context.TODO(), namespace, &client.DeleteOptions{})
			if k8serrors.IsNotFound(err) {
				return true
			}
			Expect(err).ToNot(HaveOccurred())
			return k8serrors.IsNotFound(err)
		}, 30*time.Second, time.Second).Should(BeTrue())
	})

	It("plan should be marked as not ready when VM is missing", func() {
		plan := &migrations.VirtualMachineStorageMigrationPlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-plan",
				Namespace: namespace.Name,
			},
			Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
				VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
					{
						Name: "test-vm",
						TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
							{
								VolumeName: "test-volume",
								DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
									Name:             ptr.To("test-pvc"),
									StorageClassName: ptr.To("test-storage-class"),
									AccessModes:      []migrations.VirtualMachineStorageMigrationPlanAccessMode{"ReadWriteOnce"},
									VolumeMode:       ptr.To[corev1.PersistentVolumeMode]("Filesystem"),
								},
							},
						},
					},
				},
			},
		}
		err := c.Create(context.TODO(), plan, &client.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() bool {
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: namespace.Name},
			}
			err := c.Get(context.TODO(), client.ObjectKeyFromObject(plan), plan, &client.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			return plan.Status.HasCondition(migrations.PlanNotReady)
		}, 30*time.Second, time.Second).Should(BeFalse())
	})

	DescribeTable("should reject plans with duplicate VM names",
		func(createPlan func(ns string) client.Object) {
			plan := createPlan(namespace.Name)
			err := c.Create(context.TODO(), plan, &client.CreateOptions{})
			Expect(err).To(HaveOccurred())
			Expect(k8serrors.IsInvalid(err)).To(BeTrue(), "expected Invalid error, got: %v", err)
			Expect(err.Error()).To(ContainSubstring("Duplicate value"))
		},
		Entry("regular migration plan", func(ns string) client.Object {
			return &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-plan-duplicates",
					Namespace: ns,
				},
				Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
					VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
						{
							Name: "test-vm-1",
							TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
								{
									VolumeName: "disk0",
									DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
										Name:             ptr.To("test-pvc-1"),
										StorageClassName: ptr.To("test-storage-class"),
										AccessModes:      []migrations.VirtualMachineStorageMigrationPlanAccessMode{"ReadWriteOnce"},
										VolumeMode:       ptr.To[corev1.PersistentVolumeMode]("Filesystem"),
									},
								},
							},
						},
						{
							Name: "test-vm-2",
							TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
								{
									VolumeName: "disk0",
									DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
										Name:             ptr.To("test-pvc-2"),
										StorageClassName: ptr.To("test-storage-class"),
										AccessModes:      []migrations.VirtualMachineStorageMigrationPlanAccessMode{"ReadWriteOnce"},
										VolumeMode:       ptr.To[corev1.PersistentVolumeMode]("Filesystem"),
									},
								},
							},
						},
						{
							Name: "test-vm-1",
							TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
								{
									VolumeName: "disk1",
									DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
										Name:             ptr.To("test-pvc-3"),
										StorageClassName: ptr.To("test-storage-class"),
										AccessModes:      []migrations.VirtualMachineStorageMigrationPlanAccessMode{"ReadWriteOnce"},
										VolumeMode:       ptr.To[corev1.PersistentVolumeMode]("Filesystem"),
									},
								},
							},
						},
					},
				},
			}
		}),
		Entry("multi-namespace migration plan", func(ns string) client.Object {
			return &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-multinamespace-plan-duplicates",
					Namespace: ns,
				},
				Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
					Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
						{
							Name: ns,
							VirtualMachineStorageMigrationPlanSpec: &migrations.VirtualMachineStorageMigrationPlanSpec{
								VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
									{
										Name: "test-vm-1",
										TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
											{
												VolumeName: "disk0",
												DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
													Name:             ptr.To("test-pvc-1"),
													StorageClassName: ptr.To("test-storage-class"),
													AccessModes:      []migrations.VirtualMachineStorageMigrationPlanAccessMode{"ReadWriteOnce"},
													VolumeMode:       ptr.To[corev1.PersistentVolumeMode]("Filesystem"),
												},
											},
										},
									},
									{
										Name: "test-vm-1",
										TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
											{
												VolumeName: "disk1",
												DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
													Name:             ptr.To("test-pvc-2"),
													StorageClassName: ptr.To("test-storage-class"),
													AccessModes:      []migrations.VirtualMachineStorageMigrationPlanAccessMode{"ReadWriteOnce"},
													VolumeMode:       ptr.To[corev1.PersistentVolumeMode]("Filesystem"),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
		}),
	)

	Context("offline migration", func() {
		const (
			planName      = "e2e-storage-mig-plan"
			migrationName = "e2e-storage-migration"
			volumeName    = "disk0"
		)

		var (
			namespace        *corev1.Namespace
			storageClassName string
		)

		BeforeEach(func() {
			namespace, storageClassName = setupNamespaceAndStorageClassHelper("e2e-storage-mig-")
		})

		AfterEach(func() {
			By("Deleting migration if present")
			migration := &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name},
			}
			err := c.Delete(context.TODO(), migration, &client.DeleteOptions{})
			if !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			By("Deleting plan if present")
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace.Name},
			}
			err = c.Delete(context.TODO(), plan, &client.DeleteOptions{})
			if !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			cleanupNamespaceHelper(namespace)
		})

		createDVSpec := func(sc, size string) *cdiv1.DataVolume {
			dv := libdv.NewDataVolume(
				libdv.WithNamespace(namespace.Name),
				libdv.WithRegistryURLSourceAndCustomCA(
					cd.DataVolumeImportUrlForContainerDisk(cd.ContainerDiskCirros), registryProxyCACertName),
				libdv.WithStorage(libdv.StorageWithStorageClass(sc),
					libdv.StorageWithVolumeSize(size),
					libdv.StorageWithFilesystemVolumeMode(),
				),
			)
			return dv
		}

		createVMWithDV := func(dv *cdiv1.DataVolume,
			runStrategy virtv1.VirtualMachineRunStrategy) *virtv1.VirtualMachine {
			vmi := libvmi.New(
				libvmi.WithNamespace(dv.Namespace),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(virtv1.DefaultPodNetwork()),
				libvmi.WithMemoryRequest("128Mi"),
				libvmi.WithDataVolume("disk0", dv.Name),
				libvmi.WithCloudInitNoCloud(libvmifact.WithDummyCloudForFastBoot()),
			)
			vm := libvmi.NewVirtualMachine(vmi,
				libvmi.WithRunStrategy(runStrategy),
				libvmi.WithDataVolumeTemplate(dv),
			)
			vm.Namespace = dv.Namespace
			err := c.Create(context.Background(), vm, &client.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			if runStrategy == virtv1.RunStrategyAlways || runStrategy == virtv1.RunStrategyRerunOnFailure {
				Eventually(matcher.ThisVM(vm, c), 360*time.Second, 1*time.Second).Should(matcher.BeReady())
				By(fmt.Sprintf("Waiting for VMI %s to be ready", vmi.Name))
				vmi = &virtv1.VirtualMachineInstance{
					ObjectMeta: metav1.ObjectMeta{Name: vmi.Name, Namespace: vmi.Namespace},
				}
				err = c.Get(context.TODO(), client.ObjectKeyFromObject(vmi), vmi, &client.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				libwait.WaitForSuccessfulVMIStart(vmi, c)
				By("logging in to the VMI")
				Expect(console.LoginToCirros(vmi)).To(Succeed())
			}

			return vm
		}

		// createPlanAndMigration creates the plan, waits for it to be ready, creates the migration, and returns.
		createPlanAndMigration := func(vmNames []string, targetPVCNames []string, namespace string, expectedReady int) {
			Expect(vmNames).To(HaveLen(len(targetPVCNames)))
			plan := &migrations.VirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace},
				Spec: migrations.VirtualMachineStorageMigrationPlanSpec{
					VirtualMachines: make([]migrations.VirtualMachineStorageMigrationPlanVirtualMachine, 0, len(vmNames)),
				},
			}
			for i, vmName := range vmNames {
				plan.Spec.VirtualMachines = append(plan.Spec.VirtualMachines,
					migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
						Name: vmName,
						TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
							{
								VolumeName: volumeName,
								DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
									Name:             ptr.To(targetPVCNames[i]),
									StorageClassName: &storageClassName,
									AccessModes: []migrations.VirtualMachineStorageMigrationPlanAccessMode{
										migrations.VirtualMachineStorageMigrationPlanAccessMode(corev1.ReadWriteOnce)},
									VolumeMode: ptr.To(corev1.PersistentVolumeMode("Filesystem")),
								},
							},
						},
					})
			}
			err := c.Create(context.TODO(), plan, &client.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for plan to have Ready condition and expected ReadyMigrations")
			Eventually(func(g Gomega) {
				p := &migrations.VirtualMachineStorageMigrationPlan{
					ObjectMeta: metav1.ObjectMeta{Name: planName, Namespace: namespace},
				}
				getErr := c.Get(context.TODO(), client.ObjectKeyFromObject(p), p, &client.GetOptions{})
				g.Expect(getErr).NotTo(HaveOccurred())
				c := p.Status.FindCondition(migrations.Ready)
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.Status).To(Equal(corev1.ConditionTrue), "plan Ready condition: %s", c.Message)
				g.Expect(len(p.Status.ReadyMigrations)).To(BeNumerically(">=", expectedReady),
					"ReadyMigrations: %d", len(p.Status.ReadyMigrations))
			}, 5*time.Minute, 5*time.Second).Should(Succeed())

			migration := &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace},
				Spec: migrations.VirtualMachineStorageMigrationSpec{
					VirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
						Name:      planName,
						Namespace: namespace,
					},
				},
			}
			Expect(c.Create(context.TODO(), migration, &client.CreateOptions{})).To(Succeed())
		}

		waitMigrationCompleted := func(expectedVMNames []string, namespace string) {
			By("Waiting for migration to complete with all VMs in CompletedMigrations")
			Eventually(func(g Gomega) {
				m := &migrations.VirtualMachineStorageMigration{
					ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace},
				}
				Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(m), m, &client.GetOptions{})).To(Succeed())
				g.Expect(m.Status.Phase).To(Equal(migrations.Completed), "phase=%s", m.Status.Phase)
				g.Expect(m.Status.CompletedMigrations).To(ConsistOf(expectedVMNames), "completed=%v", m.Status.CompletedMigrations)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())
		}

		verifyRunningVM := func(vm *virtv1.VirtualMachine, targetPVCName string) {
			By("Checking the running VM is using the new PVC")
			runningVM := &virtv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: vm.Namespace},
			}
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(runningVM), runningVM, &client.GetOptions{})).To(Succeed())
			Expect(runningVM.Spec.DataVolumeTemplates[0].Name).To(Equal(targetPVCName))
			Expect(runningVM.Spec.Template.Spec.Volumes[0].DataVolume.Name).To(Equal(targetPVCName))
			By("Checking the running VM is still running, by logging in to it")
			runningVMI := &virtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: vm.Namespace},
			}
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(runningVMI), runningVMI, &client.GetOptions{})).To(Succeed())
			Expect(console.LoginToCirros(runningVMI)).To(Succeed())
		}

		verifyOfflineVM := func(vm *virtv1.VirtualMachine, targetPVCName string) {
			By("Checking the offline VM is using the new PVC")
			offlineVM := &virtv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: vm.Namespace},
			}
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(offlineVM), offlineVM, &client.GetOptions{})).To(Succeed())
			Expect(offlineVM.Spec.DataVolumeTemplates[0].Name).To(Equal(targetPVCName))
			Expect(offlineVM.Spec.Template.Spec.Volumes[0].DataVolume.Name).To(Equal(targetPVCName))
		}

		It("should successfully migrate a plan with only offline VMs", func() {
			target1, target2 := "target-pvc-1", "target-pvc-2"

			dv1 := createDVSpec(storageClassName, cd.CirrosVolumeSize)
			dv2 := createDVSpec(storageClassName, cd.CirrosVolumeSize)
			vm1 := createVMWithDV(dv1, virtv1.RunStrategyHalted)
			vm2 := createVMWithDV(dv2, virtv1.RunStrategyHalted)

			createPlanAndMigration([]string{vm1.Name, vm2.Name}, []string{target1, target2}, namespace.Name, 2)
			waitMigrationCompleted([]string{vm1.Name, vm2.Name}, namespace.Name)
			verifyOfflineVM(vm1, target1)
			verifyOfflineVM(vm2, target2)
		})

		It("should cancel when the migration is deleted mid-flight and keep the VM on the source PVC", func() {
			const (
				targetPVC      = "target-pvc-cancel"
				policyLabelKey = "e2e-storage-mig-cancel"
				policyLabelVal = "slow"
			)
			completionTimeoutPerGiB := int64(800)
			var migrationPolicyName string
			DeferCleanup(func() {
				if migrationPolicyName == "" {
					return
				}
				By("Deleting MigrationPolicy")
				policy := &migrationsv1.MigrationPolicy{
					ObjectMeta: metav1.ObjectMeta{Name: migrationPolicyName},
				}
				err := c.Delete(context.TODO(), policy, &client.DeleteOptions{})
				if !k8serrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred())
				}
			})

			By("Creating a Fedora DataVolume (stress-ng available for dirtying memory)")
			dv := libdv.NewDataVolume(
				libdv.WithNamespace(namespace.Name),
				libdv.WithRegistryURLSourceAndCustomCA(
					cd.DataVolumeImportUrlForContainerDisk(cd.ContainerDiskFedoraTestTooling), registryProxyCACertName),
				libdv.WithStorage(
					libdv.StorageWithStorageClass(storageClassName),
					libdv.StorageWithVolumeSize(cd.FedoraVolumeSize),
					libdv.StorageWithFilesystemVolumeMode(),
				),
			)
			sourceDVName := dv.Name

			By("Creating a running Fedora VM labeled for a bandwidth-limited MigrationPolicy")
			vmi := libvmi.New(
				libvmi.WithNamespace(namespace.Name),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(virtv1.DefaultPodNetwork()),
				libvmi.WithMemoryRequest("1Gi"),
				libvmi.WithDataVolume(volumeName, dv.Name),
				libvmi.WithLabel(policyLabelKey, policyLabelVal),
			)
			vm := libvmi.NewVirtualMachine(vmi,
				libvmi.WithRunStrategy(virtv1.RunStrategyAlways),
				libvmi.WithDataVolumeTemplate(dv),
			)
			vm.Namespace = namespace.Name
			Expect(c.Create(context.Background(), vm, &client.CreateOptions{})).To(Succeed())

			By("Creating a MigrationPolicy that throttles live migration bandwidth")
			migrationPolicyName = fmt.Sprintf("e2e-cancel-%s", rand.String(5))
			policy := &migrationsv1.MigrationPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: migrationPolicyName},
				Spec: migrationsv1.MigrationPolicySpec{
					BandwidthPerMigration:   ptr.To(resource.MustParse("1Ki")),
					CompletionTimeoutPerGiB: &completionTimeoutPerGiB,
					Selectors: &migrationsv1.Selectors{
						VirtualMachineInstanceSelector: migrationsv1.LabelSelector{
							policyLabelKey: policyLabelVal,
						},
					},
				},
			}
			Expect(c.Create(context.TODO(), policy, &client.CreateOptions{})).To(Succeed())

			By("Waiting for the VM/VMI to be ready")
			Eventually(matcher.ThisVM(vm, c), 360*time.Second, 1*time.Second).Should(matcher.BeReady())
			runningVMI := &virtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: vm.Namespace},
			}
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(runningVMI), runningVMI, &client.GetOptions{})).To(Succeed())
			libwait.WaitForSuccessfulVMIStart(runningVMI, c)

			By("Logging in and starting stress-ng to keep memory dirty during migration")
			Expect(console.LoginToFedora(runningVMI)).To(Succeed())
			Expect(console.ExpectBatch(runningVMI, []expect.Batcher{
				&expect.BSnd{S: "\n"},
				&expect.BExp{R: console.PromptExpression},
				&expect.BSnd{S: "command -v stress-ng\n"},
				&expect.BExp{R: console.PromptExpression},
				&expect.BSnd{S: "stress-ng --vm 1 --vm-bytes 250M --vm-keep &\n"},
				&expect.BExp{R: console.PromptExpression},
			}, 60*time.Second)).To(Succeed())

			createPlanAndMigration([]string{vm.Name}, []string{targetPVC}, namespace.Name, 1)

			By("Waiting for migration to be mid-flight with a live VirtualMachineInstanceMigration")
			Eventually(func(g Gomega) {
				m := &migrations.VirtualMachineStorageMigration{
					ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name},
				}
				g.Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(m), m, &client.GetOptions{})).To(Succeed())
				g.Expect(m.Status.Phase).To(Equal(migrations.WaitForLiveMigrationToComplete), "phase=%s", m.Status.Phase)
				g.Expect(m.Status.RunningMigrations).NotTo(BeEmpty())
				g.Expect(m.Finalizers).To(ContainElement(migrations.VirtualMachineStorageMigrationFinalizer),
					"migration must have a finalizer so delete goes through cancel")

				vmimList := &virtv1.VirtualMachineInstanceMigrationList{}
				g.Expect(c.List(context.TODO(), vmimList, client.InNamespace(namespace.Name))).To(Succeed())
				g.Expect(vmimList.Items).NotTo(BeEmpty(), "expected an in-progress VirtualMachineInstanceMigration")
			}, 10*time.Minute, 2*time.Second).Should(Succeed())

			By("Deleting the migration while in progress")
			migration := &migrations.VirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name},
			}
			Expect(c.Delete(context.TODO(), migration, &client.DeleteOptions{})).To(Succeed())

			By("Waiting for migration cancel to finish and the object to be garbage-collected")
			Eventually(func(g Gomega) {
				m := &migrations.VirtualMachineStorageMigration{
					ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name},
				}
				err := c.Get(context.TODO(), client.ObjectKeyFromObject(m), m, &client.GetOptions{})
				if k8serrors.IsNotFound(err) {
					return
				}
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(m.DeletionTimestamp).NotTo(BeNil(), "expected DeletionTimestamp after delete")
				g.Expect(m.Status.Phase).NotTo(Equal(migrations.Completed),
					"migration completed instead of canceling; phase=%s cancelled=%v running=%v",
					m.Status.Phase, m.Status.CancelledMigrations, m.Status.RunningMigrations)
				g.Expect(m.Status.Phase).To(Or(
					Equal(migrations.Canceling),
					Equal(migrations.CleanupCancelledMigrations),
					Equal(migrations.Canceled),
				), "expected cancel pipeline while finalizer is held; phase=%s cancelled=%v running=%v",
					m.Status.Phase, m.Status.CancelledMigrations, m.Status.RunningMigrations)
				// Object still exists (Get succeeded above). Fail so Eventually retries
				// until NotFound hits the early return.
				g.Expect("object still exists").To(BeEmpty(),
					"waiting for GC after cancel; phase=%s cancelled=%v running=%v",
					m.Status.Phase, m.Status.CancelledMigrations, m.Status.RunningMigrations)
			}, 15*time.Minute, 2*time.Second).Should(Succeed())

			By("Verifying the VM still references the source DataVolume and remains accessible")
			updatedVM := &virtv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{Name: vm.Name, Namespace: vm.Namespace},
			}
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(updatedVM), updatedVM, &client.GetOptions{})).To(Succeed())
			Expect(updatedVM.Spec.DataVolumeTemplates[0].Name).To(Equal(sourceDVName))
			Expect(updatedVM.Spec.Template.Spec.Volumes[0].DataVolume.Name).To(Equal(sourceDVName))
			Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(runningVMI), runningVMI, &client.GetOptions{})).To(Succeed())
			Expect(console.LoginToFedora(runningVMI)).To(Succeed())
		})

		It("should successfully migrate a plan with one running and one offline VM", func() {
			targetRunning, targetOffline := "target-running", "target-offline"

			dv1 := createDVSpec(storageClassName, cd.CirrosVolumeSize)
			dv2 := createDVSpec(storageClassName, cd.CirrosVolumeSize)
			vmRunning := createVMWithDV(dv1, virtv1.RunStrategyAlways)
			vmOffline := createVMWithDV(dv2, virtv1.RunStrategyHalted)

			createPlanAndMigration([]string{vmRunning.Name, vmOffline.Name},
				[]string{targetRunning, targetOffline}, namespace.Name, 2)
			waitMigrationCompleted([]string{vmRunning.Name, vmOffline.Name}, namespace.Name)
			verifyRunningVM(vmRunning, targetRunning)
			verifyOfflineVM(vmOffline, targetOffline)
		})
	})

	Context("multi-namespace live migration with deleteSource", func() {
		const (
			multiNsPlanName = "e2e-multinamespace-plan"
			migrationName   = "e2e-multinamespace-migration"
			bootVolumeName  = "rootdisk"
			dataVolumeName  = "datadisk"
		)

		var (
			namespace        *corev1.Namespace
			storageClassName string
		)

		BeforeEach(func() {
			namespace, storageClassName = setupNamespaceAndStorageClassHelper("e2e-multins-mig-")
		})

		AfterEach(func() {
			By("Deleting migration if present")
			migration := &migrations.MultiNamespaceVirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name},
			}
			err := c.Delete(context.TODO(), migration, &client.DeleteOptions{})
			if !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			By("Deleting multi-namespace plan if present")
			plan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: multiNsPlanName, Namespace: namespace.Name},
			}
			err = c.Delete(context.TODO(), plan, &client.DeleteOptions{})
			if !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}

			cleanupNamespaceHelper(namespace)
		})

		It("should live migrate a VM with multiple disks and delete source PVCs", func() {
			vmName := "test-vm-multidisk"
			bootDVName := vmName + "-boot"
			dataDVName := vmName + "-data"

			By("Creating boot disk DataVolume")
			bootDV := libdv.NewDataVolume(
				libdv.WithName(bootDVName),
				libdv.WithNamespace(namespace.Name),
				libdv.WithRegistryURLSourceAndCustomCA(
					cd.DataVolumeImportUrlForContainerDisk(cd.ContainerDiskCirros), registryProxyCACertName),
				libdv.WithStorage(
					libdv.StorageWithStorageClass(storageClassName),
					libdv.StorageWithVolumeSize(cd.CirrosVolumeSize),
					libdv.StorageWithFilesystemVolumeMode(),
				),
			)

			By("Creating data disk DataVolume")
			dataDV := libdv.NewDataVolume(
				libdv.WithName(dataDVName),
				libdv.WithNamespace(namespace.Name),
				libdv.WithBlankImageSource(),
				libdv.WithStorage(
					libdv.StorageWithStorageClass(storageClassName),
					libdv.StorageWithVolumeSize("1Gi"),
					libdv.StorageWithFilesystemVolumeMode(),
				),
			)

			By("Creating VM with boot disk and data disk")
			vmi := libvmi.New(
				libvmi.WithNamespace(namespace.Name),
				libvmi.WithInterface(libvmi.InterfaceDeviceWithMasqueradeBinding()),
				libvmi.WithNetwork(virtv1.DefaultPodNetwork()),
				libvmi.WithMemoryRequest("128Mi"),
				libvmi.WithDataVolume(bootVolumeName, bootDV.Name),
				libvmi.WithDataVolume(dataVolumeName, dataDV.Name),
				libvmi.WithCloudInitNoCloud(libvmifact.WithDummyCloudForFastBoot()),
			)
			vm := libvmi.NewVirtualMachine(vmi,
				libvmi.WithRunStrategy(virtv1.RunStrategyAlways),
				libvmi.WithDataVolumeTemplate(bootDV),
				libvmi.WithDataVolumeTemplate(dataDV),
			)
			vm.Name = vmName
			vm.Namespace = namespace.Name

			err := c.Create(context.Background(), vm, &client.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for VM to be ready")
			Eventually(matcher.ThisVM(vm, c), 360*time.Second, 1*time.Second).Should(matcher.BeReady())

			By("Waiting for VMI to start successfully")
			vmi = &virtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{Name: vmName, Namespace: namespace.Name},
			}
			err = c.Get(context.TODO(), client.ObjectKeyFromObject(vmi), vmi, &client.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			libwait.WaitForSuccessfulVMIStart(vmi, c)

			By("Logging in to the VMI to verify it's running")
			Expect(console.LoginToCirros(vmi)).To(Succeed())

			// Store original source PVC names
			originalBootPVC := bootDV.Name
			originalDataPVC := dataDV.Name

			By("Creating MultiNamespaceVirtualMachineStorageMigrationPlan with deleteSource retention policy")
			plan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      multiNsPlanName,
					Namespace: namespace.Name,
				},
				Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
					RetentionPolicy: ptr.To(migrations.RetentionPolicyDeleteSource),
					Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
						{
							Name: namespace.Name,
							VirtualMachineStorageMigrationPlanSpec: &migrations.VirtualMachineStorageMigrationPlanSpec{
								RetentionPolicy: ptr.To(migrations.RetentionPolicyDeleteSource),
								VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
									{
										Name: vmName,
										TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
											{
												VolumeName: bootVolumeName,
												DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
													StorageClassName: &storageClassName,
													AccessModes: []migrations.VirtualMachineStorageMigrationPlanAccessMode{
														migrations.VirtualMachineStorageMigrationPlanAccessMode(corev1.ReadWriteOnce),
													},
													VolumeMode: ptr.To(corev1.PersistentVolumeMode("Filesystem")),
												},
											},
											{
												VolumeName: dataVolumeName,
												DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
													StorageClassName: &storageClassName,
													AccessModes: []migrations.VirtualMachineStorageMigrationPlanAccessMode{
														migrations.VirtualMachineStorageMigrationPlanAccessMode(corev1.ReadWriteOnce),
													},
													VolumeMode: ptr.To(corev1.PersistentVolumeMode("Filesystem")),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			err = c.Create(context.TODO(), plan, &client.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for plan to be ready")
			Eventually(func(g Gomega) {
				By(fmt.Sprintf("Getting plan: %s/%s", namespace.Name, multiNsPlanName))
				p := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}
				planKey := client.ObjectKey{Name: multiNsPlanName, Namespace: namespace.Name}
				getErr := c.Get(context.TODO(), planKey, p, &client.GetOptions{})
				g.Expect(getErr).NotTo(HaveOccurred(), "error getting plan: %v", getErr)
				g.Expect(p.Status.Namespaces).NotTo(BeEmpty(), "plan has no namespace status entries")
				By(fmt.Sprintf("Checking namespace plan conditions: %#v", p.Status.Namespaces[0].Conditions))
				cond := p.Status.Namespaces[0].FindCondition(migrations.Ready)
				By(fmt.Sprintf("Checking plan Ready condition: %v", cond))
				g.Expect(cond).NotTo(BeNil(), "plan Ready condition not found in namespace status")
				g.Expect(cond.Status).To(Equal(corev1.ConditionTrue), "plan Ready condition: %s", cond.Message)
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("Creating MultiNamespaceVirtualMachineStorageMigration")
			migration := &migrations.MultiNamespaceVirtualMachineStorageMigration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      migrationName,
					Namespace: namespace.Name,
				},
				Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationSpec{
					MultiNamespaceVirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
						Name:      multiNsPlanName,
						Namespace: namespace.Name,
					},
				},
			}
			err = c.Create(context.TODO(), migration, &client.CreateOptions{})
			Expect(err).To(Succeed())

			By("Waiting for migration to complete")
			Eventually(func(g Gomega) {
				m := &migrations.MultiNamespaceVirtualMachineStorageMigration{
					ObjectMeta: metav1.ObjectMeta{Name: migrationName, Namespace: namespace.Name},
				}
				Expect(c.Get(context.TODO(), client.ObjectKeyFromObject(m), m, &client.GetOptions{})).To(Succeed())
				g.Expect(m.Status.Namespaces).ToNot(BeEmpty(), "expected at least 1 namespace status")
				nsStatus := m.Status.Namespaces[0]
				g.Expect(nsStatus.Phase).To(Equal(migrations.Completed), "phase=%s", nsStatus.Phase)
				g.Expect(nsStatus.CompletedMigrations).To(ContainElement(vmName), "completed=%v", nsStatus.CompletedMigrations)
			}, 10*time.Minute, 15*time.Second).Should(Succeed())

			By("Verifying VM is still running and using new PVCs")
			migratedVM := &virtv1.VirtualMachine{
				ObjectMeta: metav1.ObjectMeta{Name: vmName, Namespace: namespace.Name},
			}
			err = c.Get(context.TODO(), client.ObjectKeyFromObject(migratedVM), migratedVM, &client.GetOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Verify VM is using new DVs (not the original ones)
			Expect(migratedVM.Spec.DataVolumeTemplates).To(HaveLen(2))
			Expect(migratedVM.Spec.DataVolumeTemplates[0].Name).NotTo(Equal(originalBootPVC))
			Expect(migratedVM.Spec.DataVolumeTemplates[1].Name).NotTo(Equal(originalDataPVC))

			// Verify the VM is using the new DataVolumes
			originalDVs := []string{originalBootPVC, originalDataPVC}
			for _, volume := range migratedVM.Spec.Template.Spec.Volumes {
				if volume.DataVolume != nil {
					Expect(volume.DataVolume.Name).NotTo(BeElementOf(originalDVs))
				}
			}

			By("Verifying VM is still accessible after migration")
			migratedVMI := &virtv1.VirtualMachineInstance{
				ObjectMeta: metav1.ObjectMeta{Name: vmName, Namespace: namespace.Name},
			}
			err = c.Get(context.TODO(), client.ObjectKeyFromObject(migratedVMI), migratedVMI, &client.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(console.LoginToCirros(migratedVMI)).To(Succeed())

			By("Verifying source DataVolumes are deleted")
			Eventually(func(g Gomega) {
				bootDVCheck := &cdiv1.DataVolume{
					ObjectMeta: metav1.ObjectMeta{Name: originalBootPVC, Namespace: namespace.Name},
				}
				err := c.Get(context.TODO(), client.ObjectKeyFromObject(bootDVCheck), bootDVCheck, &client.GetOptions{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "boot DV %s should be deleted", originalBootPVC)

				dataDVCheck := &cdiv1.DataVolume{
					ObjectMeta: metav1.ObjectMeta{Name: originalDataPVC, Namespace: namespace.Name},
				}
				err = c.Get(context.TODO(), client.ObjectKeyFromObject(dataDVCheck), dataDVCheck, &client.GetOptions{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "data DV %s should be deleted", originalDataPVC)
			}, 1*time.Minute, 5*time.Second).Should(Succeed())

			By("Verifying source PVCs are deleted (due to deleteSource retention policy)")
			Eventually(func(g Gomega) {
				bootPVC := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: originalBootPVC, Namespace: namespace.Name},
				}
				err := c.Get(context.TODO(), client.ObjectKeyFromObject(bootPVC), bootPVC, &client.GetOptions{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "boot PVC %s should be deleted", originalBootPVC)

				dataPVC := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: originalDataPVC, Namespace: namespace.Name},
				}
				err = c.Get(context.TODO(), client.ObjectKeyFromObject(dataPVC), dataPVC, &client.GetOptions{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "data PVC %s should be deleted", originalDataPVC)
			}, 1*time.Minute, 5*time.Second).Should(Succeed())
		})
	})

})
