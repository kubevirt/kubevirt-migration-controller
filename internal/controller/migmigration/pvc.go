package migmigration

import (
	"context"
)

// PVCNameMapping is a mapping for source -> destination pvc names
// used for convenience to avoid nested lookups to find migrated PVC names
// type pvcNameMapping map[string]string

// Add adds a new PVC to mapping
// func (p pvcNameMapping) Add(namespace string, srcName string, destName string) {
// 	if p == nil {
// 		p = make(pvcNameMapping)
// 	}
// 	key := fmt.Sprintf("%s/%s", namespace, srcName)
// 	p[key] = destName
// }

// Get given a source PVC namespace and name, returns associated destination PVC name and ns
// func (p pvcNameMapping) Get(namespace string, srcName string) (string, bool) {
// 	key := fmt.Sprintf("%s/%s", namespace, srcName)
// 	val, exists := p[key]
// 	return val, exists
// }

// ExistsAsValue given a PVC name, tells whether it exists as a destination name
// func (p pvcNameMapping) ExistsAsValue(destName string) bool {
// 	for _, v := range p {
// 		if destName == v {
// 			return true
// 		}
// 	}
// 	return false
// }

// swapPVCReferences for storage conversion migrations, this method
// swaps the existing PVC references on workload resources with the
// new pvcs created during storage migration
func (t *Task) swapPVCReferences(ctx context.Context) (reasons []string, err error) {
	// build a mapping of source to destination pvc names to avoid nested loops
	// mapping := t.getPVCNameMapping()
	// failedVirtualMachineSwaps := t.swapVirtualMachinePVCRefs(ctx, t.Client, mapping)
	// if len(failedVirtualMachineSwaps) > 0 {
	// 	reasons = append(reasons,
	// 		fmt.Sprintf("Failed updating PVC references on VirtualMachines [%s]", strings.Join(failedVirtualMachineSwaps, ",")))
	// }

	// failedHandleSourceLabels := t.handleSourceLabels(t.Client, mapping)
	// if len(failedHandleSourceLabels) > 0 {
	// 	reasons = append(reasons,
	// 		fmt.Sprintf("Failed updating labels on source PVCs [%s]", strings.Join(failedHandleSourceLabels, ",")))
	// }

	return
}

// func (t *Task) handleSourceLabels(client k8sclient.Client, mapping pvcNameMapping) (failedPVCs []string) {
// 	list := corev1.PersistentVolumeClaimList{}
// 	options := k8sclient.InNamespace(t.PlanResources.MigPlan.Namespace)
// 	err := client.List(
// 		context.TODO(),
// 		&list,
// 		options)
// 	if err != nil {
// 		failedPVCs = append(failedPVCs, fmt.Sprintf("failed listing PVCs in namespace %s", t.PlanResources.MigPlan.Namespace))
// 	}
// 	for _, pvc := range list.Items {
// 		labels := pvc.Labels
// 		if labels == nil {
// 			labels = make(map[string]string)
// 		}
// 		if mapping.ExistsAsValue(pvc.Name) {
// 			// Skip target PVCs if they are in the same namespace, ensure the label was not copied
// 			// from the source PVC
// 			delete(labels, "migration.openshift.io/source-for-directvolumemigration")
// 		} else {
// 			// Migration completed successfully, mark PVCs as migrated.
// 			labels["migration.openshift.io/source-for-directvolumemigration"] = string(t.PlanResources.MigPlan.UID)
// 		}
// 		pvc.Labels = labels
// 		if err := client.Update(context.TODO(), &pvc); err != nil && !errors.IsConflict(err) && !errors.IsNotFound(err) {
// 			failedPVCs = append(failedPVCs, fmt.Sprintf("failed to modify labels on PVC %s/%s", pvc.Namespace, pvc.Name))
// 		}
// 	}
// 	return failedPVCs
// }

// func (t *Task) swapVirtualMachinePVCRefs(ctx context.Context, client k8sclient.Client, mapping pvcNameMapping) ([]string, error) {
// 	list := &virtv1.VirtualMachineList{}
// 	failedVirtualMachines := []string{}
// 	if err := client.List(context.TODO(), list, k8sclient.InNamespace(t.PlanResources.MigPlan.Namespace)); err != nil {
// 		if meta.IsNoMatchError(err) {
// 			return failedVirtualMachines, nil
// 		}
// 		return failedVirtualMachines, err
// 	}
// 	for _, vm := range list.Items {
// 		active, err := isVMActive(ctx, &vm, client)
// 		if err != nil {
// 			failedVirtualMachines = append(failedVirtualMachines, "error checking if VM is active")
// 			return failedVirtualMachines, err
// 		}
// 		if active {
// 			continue
// 		}
// 		retryCount := 1
// 		retry := true
// 		for retry && retryCount <= 3 {
// 			message, err := t.swapVirtualMachinePVCRef(client, &vm, mapping)
// 			if err != nil && !errors.IsConflict(err) {
// 				failedVirtualMachines = append(failedVirtualMachines, message)
// 				return failedVirtualMachines, err
// 			} else if errors.IsConflict(err) {
// 				// Conflict, reload VM and try again
// 				if err := client.Get(context.TODO(), k8sclient.ObjectKey{Namespace: vm.Namespace, Name: vm.Name}, &vm); err != nil {
// 					failedVirtualMachines = append(failedVirtualMachines, fmt.Sprintf("failed reloading %s/%s", vm.Namespace, vm.Name))
// 				}
// 				retryCount++
// 			} else {
// 				retry = false
// 				if message != "" {
// 					failedVirtualMachines = append(failedVirtualMachines, message)
// 				}
// 			}
// 		}
// 	}
// 	return failedVirtualMachines, nil
// }

// func (t *Task) swapVirtualMachinePVCRef(client k8sclient.Client, vm *virtv1.VirtualMachine, mapping pvcNameMapping) (string, error) {
// 	// TODO: implement for when VM is not active (swap refs, clone volumes)
// 	return "", nil
// }

// func isVMActive(ctx context.Context, vm *virtv1.VirtualMachine, client k8sclient.Client) (bool, error) {
// 	// A VM is defined active if there is a pod associated with the VM.
// 	podList := corev1.PodList{}
// 	if err := client.List(ctx, &podList, k8sclient.InNamespace(vm.Namespace)); err != nil {
// 		return false, err
// 	}
// 	for _, pod := range podList.Items {
// 		for _, owner := range pod.OwnerReferences {
// 			if owner.Kind == "VirtualMachineInstance" && owner.Name == vm.Name {
// 				return true, nil
// 			}
// 		}
// 	}
// 	return false, nil
// }
