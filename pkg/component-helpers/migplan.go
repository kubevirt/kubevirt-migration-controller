package componenthelpers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

// Get a referenced MigPlan.
// Returns `nil` when the reference cannot be resolved.
func GetPlan(ctx context.Context, client k8sclient.Client, ref *corev1.ObjectReference) (*migrationsv1alpha1.MigPlan, error) {
	if ref == nil {
		return nil, nil
	}
	migPlan := &migrationsv1alpha1.MigPlan{}

	if err := client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, migPlan); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return migPlan, nil
}
