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
package multinamespacestoragemig

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	testutils "kubevirt.io/kubevirt-migration-controller/internal/controller/testutils"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("MultiNamespaceStorageMigration controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		var reconciler *MultiNamespaceStorageMigrationReconciler

		BeforeEach(func() {
			reconciler = &MultiNamespaceStorageMigrationReconciler{
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
				multinsMigration := createMultiNamespaceStorageMigration(name, multinsPlan.Name)
				Expect(reconciler.Client.Create(ctx, multinsPlan)).To(Succeed())
				Expect(reconciler.Client.Create(ctx, multinsMigration)).To(Succeed())
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: nn,
				})
				Expect(err).NotTo(HaveOccurred())
				mig := &migrations.VirtualMachineStorageMigration{}
				mignn := types.NamespacedName{Name: migrations.GetNamespacedPlanName(name, testutils.TestNamespace), Namespace: testutils.TestNamespace}
				Expect(reconciler.Client.Get(ctx, mignn, mig)).To(Succeed())
				Expect(mig.Name).To(HaveLen(kvalidation.DNS1123SubdomainMaxLength))
				Expect(mig.Name).To(Equal(fmt.Sprintf("%s-%s", strings.Repeat("a", 229), "df1f7590-test-namespace")))
				Expect(mig.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigrationNameAnnotation, name))
				Expect(mig.Annotations).To(HaveKeyWithValue(multiNamespaceStorageMigrationNamespaceAnnotation, testutils.TestNamespace))
				Expect(mig.Labels).ToNot(HaveKey(multiNamespaceStorageMigrationNameLabel))
				Expect(mig.Labels).ToNot(HaveKey(multiNamespaceStorageMigrationNamespaceLabel))

				Expect(reconciler.Client.Get(ctx, nn, multinsMigration)).To(Succeed())
				Expect(mig.Labels).To(HaveKeyWithValue(multiNamespaceStorageMigrationUIDLabel, BeEquivalentTo(multinsMigration.UID)))
			})
		})
	})
})

func createMultiNamespaceStorageMigration(name, planName string) *migrations.MultiNamespaceVirtualMachineStorageMigration {
	return &migrations.MultiNamespaceVirtualMachineStorageMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testutils.TestNamespace,
		},
		Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationSpec{
			MultiNamespaceVirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
				Name: planName,
			},
		},
	}
}

func createMultiNamespaceStorageMigrationPlan(name string) *migrations.MultiNamespaceVirtualMachineStorageMigrationPlan {
	return &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testutils.TestNamespace,
		},
		Spec: migrations.MultiNamespaceVirtualMachineStorageMigrationPlanSpec{
			Namespaces: []migrations.VirtualMachineStorageMigrationPlanNamespaceSpec{
				{
					Name: testutils.TestNamespace,
					VirtualMachineStorageMigrationPlanSpec: &migrations.VirtualMachineStorageMigrationPlanSpec{
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
					},
				},
			},
		},
	}
}
