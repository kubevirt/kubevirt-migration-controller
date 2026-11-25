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

package main

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-operator/api/v1alpha1"
)

func TestMain(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Main Suite")
}

var _ = Describe("checkMigControllerCRDExists", func() {
	var (
		ctx       context.Context
		scheme    *runtime.Scheme
		namespace string
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = "test-namespace"
		scheme = runtime.NewScheme()
		// Register MigController types for the fake client
		Expect(migrationsv1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	Context("when the MigController CRD exists", func() {
		It("should return true and no error when MigController resources exist", func() {
			// Create a fake MigController object
			migController := &migrationsv1alpha1.MigController{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-controller",
					Namespace: namespace,
				},
			}

			// Create fake client with the MigController
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(migController).
				Build()

			exists, err := checkMigControllerCRDExists(ctx, fakeClient, namespace)

			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue())
		})

		It("should return true and no error when CRD exists but no resources", func() {
			// Create fake client with no MigController objects (but CRD is registered in scheme)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			exists, err := checkMigControllerCRDExists(ctx, fakeClient, namespace)

			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue())
		})
	})

	Context("when the MigController CRD does not exist", func() {
		It("should return false and no error on NoMatchError", func() {
			// Create a client that returns NoMatchError on List
			noMatchErr := &meta.NoKindMatchError{
				GroupKind: schema.GroupKind{
					Group: "migrations.kubevirt.io",
					Kind:  "MigController",
				},
			}
			errorClient := &errorReturningClient{
				Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				err:    noMatchErr,
			}

			exists, err := checkMigControllerCRDExists(ctx, errorClient, namespace)

			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse())
		})
	})

	Context("when there is an error checking for the CRD", func() {
		It("should return false and the error", func() {
			// Create a client that returns an error on List
			expectedErr := apierrors.NewInternalError(fmt.Errorf("network error"))
			errorClient := &errorReturningClient{
				Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				err:    expectedErr,
			}

			exists, err := checkMigControllerCRDExists(ctx, errorClient, namespace)

			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(expectedErr))
			Expect(exists).To(BeFalse())
		})

		It("should distinguish NoMatchError from other errors", func() {
			// Create a client that returns an internal error
			internalErr := apierrors.NewInternalError(fmt.Errorf("internal server error"))
			errorClient := &errorReturningClient{
				Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
				err:    internalErr,
			}

			exists, err := checkMigControllerCRDExists(ctx, errorClient, namespace)

			Expect(err).To(HaveOccurred())
			Expect(meta.IsNoMatchError(err)).To(BeFalse())
			Expect(apierrors.IsInternalError(err)).To(BeTrue())
			Expect(exists).To(BeFalse())
		})
	})
})

// errorReturningClient wraps a client.Client and returns an error on List
type errorReturningClient struct {
	client.Client
	err error
}

func (e *errorReturningClient) List(
	ctx context.Context,
	list client.ObjectList,
	opts ...client.ListOption,
) error {
	return e.err
}
