package migmigration

import (
	"fmt"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

func (t *Task) getDirectVolumeClaimList() []migrationsv1alpha1.PVCToMigrate {
	// nsMapping := t.PlanResources.MigPlan.GetNamespaceMapping()
	var pvcList []migrationsv1alpha1.PVCToMigrate

	// for _, pv := range t.PlanResources.MigPlan.Spec.PersistentVolumes.List {
	// 	if pv.Selection.Action != migrationsv1alpha1.PvCopyAction {
	// 		continue
	// 	}
	// 	accessModes := pv.PVC.AccessModes
	// 	volumeMode := pv.PVC.VolumeMode
	// 	// if the user overrides access modes, set up the destination PVC with user-defined
	// 	// access mode
	// 	if pv.Selection.AccessMode != "" {
	// 		accessModes = []corev1.PersistentVolumeAccessMode{pv.Selection.AccessMode}
	// 	}
	// 	pvcList = append(pvcList, migrationsv1alpha1.PVCToMigrate{
	// 		ObjectReference: &corev1.ObjectReference{
	// 			Name:      pv.PVC.GetSourceName(),
	// 			Namespace: pv.PVC.Namespace,
	// 		},
	// 		TargetStorageClass: pv.Selection.StorageClass,
	// 		TargetAccessModes:  accessModes,
	// 		TargetVolumeMode:   &volumeMode,
	// 		TargetNamespace:    nsMapping[pv.PVC.Namespace],
	// 		TargetName:         pv.PVC.GetTargetName(),
	// 	})
	// }

	return pvcList
}

func (t *Task) createDirectVolumeMigration(migType *migrationsv1alpha1.DirectVolumeMigrationType) error {
	// existingDvm, err := t.getDirectVolumeMigration()
	// if err != nil {
	// 	return err
	// }
	// // If already created exit with no error
	// if existingDvm != nil {
	// 	return nil
	// }
	// t.Log.Info("Building DirectVolumeMigration resource definition")
	// dvm := t.buildDirectVolumeMigration()
	// if dvm == nil {
	// 	return errors.New("failed to build directvolumeclaim list")
	// }
	// if migType != nil {
	// 	dvm.Spec.MigrationType = migType
	// }
	t.Log.Info("Creating DirectVolumeMigration on host cluster",
		"directVolumeMigration", "BAR", "dvm", migType)
	// err = t.Client.Create(context.TODO(), dvm)
	if migType != nil {
		return fmt.Errorf("migration type not supported: %s", *migType)
	}
	return nil
}
