package migmigration

import (
	"context"
	"path"

	corev1 "k8s.io/api/core/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

// Delete temporary annotations and labels added.
func (t *Task) deleteAnnotations() error {
	// nsLists, err := t.getBothNamespaces()
	// if err != nil {
	// 	return err
	// }

	// for _, ns := range nsLists {
	// 	err = t.deletePVCAnnotations(t.Client, ns)
	// 	if err != nil {
	// 		return err
	// 	}
	// }

	return nil
}

// Delete PVC stage annotations and labels.
func (t *Task) deletePVCAnnotations(client k8sclient.Client, namespaceList []string) error {
	for _, ns := range namespaceList {
		options := k8sclient.InNamespace(ns)
		pvcList := corev1.PersistentVolumeClaimList{}
		err := client.List(context.TODO(), &pvcList, options)
		if err != nil {
			return err
		}
		for _, pvc := range pvcList.Items {
			if pvc.Spec.VolumeName == "" {
				continue
			}
			needsUpdate := false
			if pvc.Annotations != nil {
				if _, found := pvc.Annotations[migrationsv1alpha1.PvActionAnnotation]; found {
					delete(pvc.Annotations, migrationsv1alpha1.PvActionAnnotation)
					needsUpdate = true
				}
				if _, found := pvc.Annotations[migrationsv1alpha1.PvStorageClassAnnotation]; found {
					delete(pvc.Annotations, migrationsv1alpha1.PvStorageClassAnnotation)
					needsUpdate = true
				}
				if _, found := pvc.Annotations[migrationsv1alpha1.PvAccessModeAnnotation]; found {
					delete(pvc.Annotations, migrationsv1alpha1.PvAccessModeAnnotation)
					needsUpdate = true
				}
			}
			if !needsUpdate {
				continue
			}
			err = client.Update(context.TODO(), &pvc)
			if err != nil {
				return err
			}
			t.Log.Info("Annotations/Labels removed on PersistentVolumeClaim.",
				"persistentVolumeClaim", path.Join(pvc.Namespace, pvc.Name))
		}
	}

	return nil
}
