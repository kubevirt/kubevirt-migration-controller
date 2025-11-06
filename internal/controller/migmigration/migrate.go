/*
Copyright 2019 Red Hat Inc.

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

package migmigration

import (
	"context"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

// Perform the migration.
func (r *MigMigrationReconciler) migrate(ctx context.Context, plan *migrationsv1alpha1.MigPlan, migration *migrationsv1alpha1.MigMigration) (time.Duration, error) {
	log := logf.FromContext(ctx)

	// Run
	task := Task{
		Client: r.Client,
		Owner:  migration,
		Plan:   plan,
		Log:    log,
	}

	if err := task.Run(ctx); err != nil {
		if k8serrors.IsConflict(err) {
			log.V(4).Info("Conflict error during task.Run, requeueing.")
			return FastReQ, nil
		}
		return task.Requeue, err
	}

	// Result
	migration.Status.Itinerary = task.Itinerary.Name

	// Completed
	// if migration.Status.Phase == string(Completed) {
	// 	migration.Status.DeleteCondition(migrationsv1alpha1.Running)
	// 	failed := task.Owner.Status.FindCondition(migrationsv1alpha1.Failed)
	// 	warnings := task.Owner.Status.FindConditionByCategory(migrationsv1alpha1.Warn)
	// 	if failed == nil && len(warnings) == 0 {
	// 		migration.Status.SetCondition(migrationsv1alpha1.Condition{
	// 			Type:     migrationsv1alpha1.Succeeded,
	// 			Status:   True,
	// 			Reason:   string(migration.Status.Phase),
	// 			Category: Advisory,
	// 			Message:  "The migration has completed successfully.",
	// 			Durable:  true,
	// 		})
	// 	}
	// 	if failed == nil && len(warnings) > 0 {
	// 		migration.Status.SetCondition(migrationsv1alpha1.Condition{
	// 			Type:     SucceededWithWarnings,
	// 			Status:   True,
	// 			Reason:   string(migration.Status.Phase),
	// 			Category: Advisory,
	// 			Message:  "The migration has completed with warnings, please look at `Warn` conditions.",
	// 			Durable:  true,
	// 		})
	// 	}
	// 	return NoReQ, nil
	// }

	// phase, n, total := task.Itinerary.progressReport(string(migration.Status.Phase))
	// message := fmt.Sprintf("Step: %d/%d", n, total)
	// migration.Status.SetCondition(migrationsv1alpha1.Condition{
	// 	Type:     migrationsv1alpha1.Running,
	// 	Status:   True,
	// 	Reason:   phase,
	// 	Category: Advisory,
	// 	Message:  message,
	// })

	return task.Requeue, nil
}
