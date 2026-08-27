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
package multinamespacestoragemigplan

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
)

var _ = Describe("MultiNamespaceStorageMigPlan controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		var reconciler *MultiNamespaceStorageMigPlanReconciler

		BeforeEach(func() {
			reconciler = &MultiNamespaceStorageMigPlanReconciler{
				Client:        k8sClient,
				Scheme:        scheme.Scheme,
				EventRecorder: record.NewFakeRecorder(10),
				Log:           logf.Log,
			}
		})

		AfterEach(func() {
			if reconciler != nil {
				close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
				testutils.CleanupResources(ctx, reconciler.Client)
				reconciler = nil
			}
		})

		Context("Naming length constraints", func() {
			It("should respect naming length constraints for namespaced plans", func() {
				name := strings.Repeat("a", kvalidation.DNS1123SubdomainMaxLength)
				nn := types.NamespacedName{Name: name, Namespace: testutils.TestNamespace}
				multinsPlan := createMultiNamespaceStorageMigrationPlan(name)
				Expect(reconciler.Client.Create(ctx, multinsPlan)).To(Succeed())
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: nn,
				})
				Expect(err).NotTo(HaveOccurred())
				np := &migrations.VirtualMachineStorageMigrationPlan{}
				npnn := types.NamespacedName{Name: migrations.GetNamespacedPlanName(name, testutils.TestNamespace), Namespace: testutils.TestNamespace}
				Expect(reconciler.Client.Get(ctx, npnn, np)).To(Succeed())
				Expect(np.Name).To(HaveLen(kvalidation.DNS1123SubdomainMaxLength))
				Expect(np.Name).To(Equal(fmt.Sprintf("%s-%s", strings.Repeat("a", 229), "df1f7590-test-namespace")))
				Expect(np.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigPlanNameAnnotation, name))
				Expect(np.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigPlanNamespaceAnnotation, testutils.TestNamespace))
				Expect(np.Labels).ToNot(HaveKey(multiNamespaceStorageMigPlanNameLabel))
				Expect(np.Labels).ToNot(HaveKey(multiNamespaceStorageMigPlanNamespaceLabel))

				Expect(reconciler.Client.Get(ctx, nn, multinsPlan)).To(Succeed())
				Expect(np.Labels).To(HaveKeyWithValue(multiNamespaceStorageMigPlanUIDLabel, BeEquivalentTo(multinsPlan.UID)))
			})
		})
	})
})

type multiNamespacePlanOptions struct {
	name            string
	namespace       string
	uid             types.UID
	retentionPolicy *migrations.RetentionPolicy
	namespaceSpecs  []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec
}

func newMultiNamespacePlanOptions(name string) *multiNamespacePlanOptions {
	return &multiNamespacePlanOptions{
		name:      name,
		namespace: testutils.TestNamespace,
	}
}

func (o *multiNamespacePlanOptions) withUID(uid string) *multiNamespacePlanOptions {
	o.uid = types.UID(uid)
	return o
}

func (o *multiNamespacePlanOptions) withRetentionPolicy(policy migrations.RetentionPolicy) *multiNamespacePlanOptions {
	o.retentionPolicy = &policy
	return o
}

func (o *multiNamespacePlanOptions) withNamespaceSpec(spec *migrations.VirtualMachineStorageMigrationPlanSpec) *multiNamespacePlanOptions {
	o.namespaceSpecs = append(o.namespaceSpecs, migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
		Name:                                   testutils.TestNamespace,
		VirtualMachineStorageMigrationPlanSpec: spec,
	})
	return o
}

func (o *multiNamespacePlanOptions) build() *migrations.MultiNamespaceVirtualMachineStorageMigrationPlan {
	plan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.name,
			Namespace: o.namespace,
		},
		Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
			RetentionPolicy: o.retentionPolicy,
			Namespaces:      o.namespaceSpecs,
		},
	}
	if o.uid != "" {
		plan.UID = o.uid
	}
	return plan
}

// createMultiNamespaceStorageMigrationPlan creates a basic multi-namespace plan with default values
func createMultiNamespaceStorageMigrationPlan(name string) *migrations.MultiNamespaceVirtualMachineStorageMigrationPlan {
	return newMultiNamespacePlanOptions(name).
		withNamespaceSpec(&migrations.VirtualMachineStorageMigrationPlanSpec{
			VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
				{
					Name: "simple-vm",
					TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
						{
							VolumeName:     "dv-disk",
							DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{},
						},
					},
				},
			},
		}).build()
}

// newTestVMSpec creates a basic VM spec for testing
func newTestVMSpec(vmName string, retentionPolicy *migrations.RetentionPolicy) *migrations.VirtualMachineStorageMigrationPlanSpec {
	return &migrations.VirtualMachineStorageMigrationPlanSpec{
		RetentionPolicy: retentionPolicy,
		VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{
			{
				Name: vmName,
				TargetMigrationPVCs: []migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
					{
						VolumeName: "test-volume",
						DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
							Name: ptr.To("target-pvc"),
						},
					},
				},
			},
		},
	}
}

var _ = Describe("MultiNamespaceStorageMigPlan Controller", func() {
	var (
		reconciler *MultiNamespaceStorageMigPlanReconciler
	)

	ctx := context.Background()

	planName := "test-multi-plan"
	typeNamespacedName := types.NamespacedName{
		Name:      planName,
		Namespace: testutils.TestNamespace,
	}

	BeforeEach(func() {
		reconciler = &MultiNamespaceStorageMigPlanReconciler{
			Client:        k8sClient,
			Scheme:        scheme.Scheme,
			EventRecorder: record.NewFakeRecorder(10),
			Log:           logf.Log.WithName("multinamespace-migplan-test"),
		}
	})

	AfterEach(func() {
		if reconciler != nil {
			close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
			testutils.CleanupResources(ctx, reconciler.Client)
			reconciler = nil
		}
	})

	Context("RetentionPolicy", func() {
		It("should create namespaced plan with deleteSource when multinamespace plan has retentionPolicy deleteSource", func() {
			By("Creating a multinamespace plan with retentionPolicy deleteSource")
			basePlan := testutils.NewVirtualMachineStorageMigrationPlan(
				testutils.TestMigPlanName,
				testutils.NewVirtualMachine(testutils.TestVMName, testutils.TestNamespace, testutils.TestVolumeName, testutils.TestSourcePVCName),
			)
			multiPlan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      planName,
					Namespace: testutils.TestNamespace,
				},
				Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
					RetentionPolicy: ptr.To(migrations.RetentionPolicyDeleteSource),
					Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
						{
							Name:                                   testutils.TestNamespace,
							VirtualMachineStorageMigrationPlanSpec: basePlan.Spec.DeepCopy(),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, multiPlan)).To(Succeed())

			By("Reconciling the multinamespace plan")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the created namespaced plan has retentionPolicy deleteSource")
			childPlanName := migrations.GetNamespacedPlanName(planName, testutils.TestNamespace)
			childPlan := &migrations.VirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: childPlanName, Namespace: testutils.TestNamespace}, childPlan)).To(Succeed())
			Expect(childPlan.Spec.RetentionPolicy).NotTo(BeNil())
			Expect(*childPlan.Spec.RetentionPolicy).To(Equal(migrations.RetentionPolicyDeleteSource))
		})

		It("should enforce retentionPolicy immutability with type-level and field-level validation", func() {
			key := types.NamespacedName{Name: "test-resource", Namespace: "default"}
			created := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
					RetentionPolicy: ptr.To(migrations.RetentionPolicyDeleteSource),
					Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
						{
							Name: testutils.TestNamespace,
							VirtualMachineStorageMigrationPlanSpec: &migrations.VirtualMachineStorageMigrationPlanSpec{
								VirtualMachines: []migrations.VirtualMachineStorageMigrationPlanVirtualMachine{},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, created)).To(Succeed())
			existing := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, key, existing)).To(Succeed())

			By("Attempting to change value from deleteSource to keepSource - field-level validation should fail")
			existing.Spec.RetentionPolicy = ptr.To(migrations.RetentionPolicyKeepSource)
			err := k8sClient.Update(ctx, existing)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("retentionPolicy value cannot be changed once set"))
			existing = &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, key, existing)).To(Succeed())
			Expect(*existing.Spec.RetentionPolicy).To(Equal(migrations.RetentionPolicyDeleteSource),
				"Value should not have changed")

			By("Attempting to remove retentionPolicy (set to nil)")
			existing.Spec.RetentionPolicy = nil
			err = k8sClient.Update(ctx, existing)

			// With CRD defaulting, nil becomes keepSource, so this triggers field-level validation
			// The error will be about value change (deleteSource -> keepSource) not removal
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Or(
				ContainSubstring("retentionPolicy value cannot be changed"),
				ContainSubstring("retentionPolicy cannot be removed"),
			), "Either field-level or type-level validation should prevent the change")
			existing = &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}
			Expect(k8sClient.Get(ctx, key, existing)).To(Succeed())
			Expect(*existing.Spec.RetentionPolicy).To(Equal(migrations.RetentionPolicyDeleteSource),
				"Value should still be deleteSource")
		})
	})

	Context("createNamespacePlan", func() {
		var (
			reconciler *MultiNamespaceStorageMigPlanReconciler
			ctx        context.Context
		)

		BeforeEach(func() {
			ctx = context.Background()
			reconciler = &MultiNamespaceStorageMigPlanReconciler{
				Client:        k8sClient,
				Scheme:        scheme.Scheme,
				EventRecorder: record.NewFakeRecorder(10),
				Log:           logf.Log.WithName("createNamespacePlan-test"),
			}
		})

		AfterEach(func() {
			if reconciler != nil {
				close(reconciler.EventRecorder.(*record.FakeRecorder).Events)
				testutils.CleanupResources(ctx, reconciler.Client)
				reconciler = nil
			}
		})

		It("should create namespace plan with basic spec", func() {
			multiPlan := newMultiNamespacePlanOptions("test-multi-plan").
				withUID("test-uid-123").
				withNamespaceSpec(newTestVMSpec("test-vm", nil)).
				build()

			err := reconciler.createNamespacePlan(ctx, multiPlan, &multiPlan.Spec.Namespaces[0])
			Expect(err).NotTo(HaveOccurred())

			createdPlan := &migrations.VirtualMachineStorageMigrationPlan{}
			planName := migrations.GetNamespacedPlanName(multiPlan.Name, testutils.TestNamespace)
			err = k8sClient.Get(ctx, types.NamespacedName{Name: planName, Namespace: testutils.TestNamespace}, createdPlan)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the spec was copied correctly")
			Expect(createdPlan.Spec.VirtualMachines).To(HaveLen(1))
			Expect(createdPlan.Spec.VirtualMachines[0].Name).To(Equal("test-vm"))
			Expect(createdPlan.Spec.VirtualMachines[0].TargetMigrationPVCs).To(HaveLen(1))
			Expect(createdPlan.Spec.VirtualMachines[0].TargetMigrationPVCs[0].VolumeName).To(Equal("test-volume"))

			By("Verifying labels are set correctly")
			Expect(createdPlan.Labels).To(HaveKeyWithValue(multiNamespaceStorageMigPlanUIDLabel, "test-uid-123"))

			By("Verifying annotations are set correctly")
			Expect(createdPlan.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigPlanNameAnnotation, "test-multi-plan"))
			Expect(createdPlan.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigPlanNamespaceAnnotation, testutils.TestNamespace))
		})

		It("should handle already existing plan gracefully", func() {
			multiPlan := newMultiNamespacePlanOptions("test-multi-plan").
				withUID("test-uid-456").
				withNamespaceSpec(newTestVMSpec("test-vm", nil)).
				build()

			By("Creating the plan the first time")
			err := reconciler.createNamespacePlan(ctx, multiPlan, &multiPlan.Spec.Namespaces[0])
			Expect(err).NotTo(HaveOccurred())

			By("Creating the plan again should not error")
			err = reconciler.createNamespacePlan(ctx, multiPlan, &multiPlan.Spec.Namespaces[0])
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("should handle retention policy correctly",
			func(multiPlanPolicy migrations.RetentionPolicy, namespacePolicy *migrations.RetentionPolicy, expectedPolicy migrations.RetentionPolicy) {
				multiPlan := newMultiNamespacePlanOptions("test-multi-plan").
					withUID("test-uid-retention").
					withRetentionPolicy(multiPlanPolicy).
					withNamespaceSpec(newTestVMSpec("test-vm", namespacePolicy)).
					build()

				err := reconciler.createNamespacePlan(ctx, multiPlan, &multiPlan.Spec.Namespaces[0])
				Expect(err).NotTo(HaveOccurred())

				createdPlan := &migrations.VirtualMachineStorageMigrationPlan{}
				planName := migrations.GetNamespacedPlanName(multiPlan.Name, testutils.TestNamespace)
				err = k8sClient.Get(ctx, types.NamespacedName{Name: planName, Namespace: testutils.TestNamespace}, createdPlan)
				Expect(err).NotTo(HaveOccurred())

				Expect(createdPlan.Spec.RetentionPolicy).NotTo(BeNil())
				Expect(*createdPlan.Spec.RetentionPolicy).To(Equal(expectedPolicy))
			},
			Entry("inherit from multi-namespace plan when namespace has default",
				migrations.RetentionPolicyDeleteSource,
				ptr.To(migrations.RetentionPolicyKeepSource),
				migrations.RetentionPolicyDeleteSource,
			),
			Entry("preserve namespace retention policy when it has explicit non-default value",
				migrations.RetentionPolicyKeepSource,
				ptr.To(migrations.RetentionPolicyDeleteSource),
				migrations.RetentionPolicyDeleteSource,
			),
			Entry("use default retention policy when both use default",
				migrations.RetentionPolicyKeepSource,
				ptr.To(migrations.RetentionPolicyKeepSource),
				migrations.RetentionPolicyKeepSource,
			),
			Entry("inherit from multi-namespace plan when namespace retention policy is nil",
				migrations.RetentionPolicyDeleteSource,
				nil,
				migrations.RetentionPolicyDeleteSource,
			),
		)

		It("should create plan with correct naming", func() {
			multiPlan := newMultiNamespacePlanOptions("my-migration-plan").
				withUID("test-uid-jkl").
				withNamespaceSpec(newTestVMSpec("test-vm", nil)).
				build()

			err := reconciler.createNamespacePlan(ctx, multiPlan, &multiPlan.Spec.Namespaces[0])
			Expect(err).NotTo(HaveOccurred())

			createdPlan := &migrations.VirtualMachineStorageMigrationPlan{}
			expectedPlanName := migrations.GetNamespacedPlanName("my-migration-plan", testutils.TestNamespace)
			err = k8sClient.Get(ctx, types.NamespacedName{Name: expectedPlanName, Namespace: testutils.TestNamespace}, createdPlan)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the plan name follows the naming convention")
			Expect(createdPlan.Name).To(Equal(expectedPlanName))
			Expect(createdPlan.Namespace).To(Equal(testutils.TestNamespace))
		})

		It("should deep copy the spec to avoid mutations", func() {
			originalVMName := "original-vm"
			multiPlan := newMultiNamespacePlanOptions("test-multi-plan").
				withUID("test-uid-mno").
				withNamespaceSpec(newTestVMSpec(originalVMName, nil)).
				build()

			err := reconciler.createNamespacePlan(ctx, multiPlan, &multiPlan.Spec.Namespaces[0])
			Expect(err).NotTo(HaveOccurred())

			By("Modifying the original spec")
			multiPlan.Spec.Namespaces[0].VirtualMachineStorageMigrationPlanSpec.VirtualMachines[0].Name = "modified-vm"

			By("Verifying the created plan was not affected by the mutation")
			createdPlan := &migrations.VirtualMachineStorageMigrationPlan{}
			planName := migrations.GetNamespacedPlanName(multiPlan.Name, testutils.TestNamespace)
			err = k8sClient.Get(ctx, types.NamespacedName{Name: planName, Namespace: testutils.TestNamespace}, createdPlan)
			Expect(err).NotTo(HaveOccurred())
			Expect(createdPlan.Spec.VirtualMachines[0].Name).To(Equal(originalVMName))
		})
	})
})
