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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
