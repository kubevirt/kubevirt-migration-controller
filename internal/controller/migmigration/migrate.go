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
	"fmt"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
)

// Perform the migration.
func (r *MigMigrationReconciler) migrate(ctx context.Context, migration *migrationsv1alpha1.MigMigration) (time.Duration, error) {
	log := logf.FromContext(ctx)

	// Ready
	plan, err := componenthelpers.GetPlan(r.Client, migration.Spec.MigPlanRef)
	if err != nil {
		return 0, fmt.Errorf("failed to get plan: %w", err)
	}
	if !plan.Status.IsReady() {
		log.Info("Plan not ready. Migration can't run unless Plan is ready.")
		return 0, nil
	}

	// Resources
	planResources, err := getReferencedResources(plan)
	if err != nil {
		return 0, fmt.Errorf("failed to get plan resources: %w", err)
	}

	// Started
	if migration.Status.StartTimestamp == nil {
		log.Info("Marking MigMigration as started.")
		migration.Status.StartTimestamp = &metav1.Time{Time: time.Now()}
	}

	// Run
	task := Task{
		Client:        r.Client,
		Owner:         migration,
		PlanResources: planResources,
		Phase:         migration.Status.Phase,
		Log:           log,
		Annotations:   r.getAnnotations(migration),
	}
	err = task.Run(ctx)
	if err != nil {
		if errors.IsConflict(err) {
			log.V(4).Info("Conflict error during task.Run, requeueing.")
			return FastReQ, nil
		}
		log.Info("Phase execution failed.",
			"phase", task.Phase,
			"error", err.Error())

		task.fail("MigrationFailed", []string{err.Error()})
		return task.Requeue, nil
	}

	// Result
	migration.Status.Phase = task.Phase
	migration.Status.Itinerary = task.Itinerary.Name

	// Completed
	if task.Phase == Completed {
		migration.Status.DeleteCondition(migrationsv1alpha1.Running)
		failed := task.Owner.Status.FindCondition(migrationsv1alpha1.Failed)
		warnings := task.Owner.Status.FindConditionByCategory(migrationsv1alpha1.Warn)
		if failed == nil && len(warnings) == 0 {
			migration.Status.SetCondition(migrationsv1alpha1.Condition{
				Type:     migrationsv1alpha1.Succeeded,
				Status:   True,
				Reason:   task.Phase,
				Category: Advisory,
				Message:  "The migration has completed successfully.",
				Durable:  true,
			})
		}
		if failed == nil && len(warnings) > 0 {
			migration.Status.SetCondition(migrationsv1alpha1.Condition{
				Type:     SucceededWithWarnings,
				Status:   True,
				Reason:   task.Phase,
				Category: Advisory,
				Message:  "The migration has completed with warnings, please look at `Warn` conditions.",
				Durable:  true,
			})
		}
		return NoReQ, nil
	}

	phase, n, total := task.Itinerary.progressReport(task.Phase)
	message := fmt.Sprintf("Step: %d/%d", n, total)
	migration.Status.SetCondition(migrationsv1alpha1.Condition{
		Type:     migrationsv1alpha1.Running,
		Status:   True,
		Reason:   phase,
		Category: Advisory,
		Message:  message,
	})

	return task.Requeue, nil
}

func getReferencedResources(plan *migrationsv1alpha1.MigPlan) (*PlanResources, error) {
	planResources := &PlanResources{
		MigPlan: plan,
	}
	if plan == nil {
		return nil, fmt.Errorf("plan is nil")
	}
	return planResources, nil
}

// Get annotations.
// TODO: Revisit this. We are hardcoding this for now until 2 things occur.
// 1. We are properly setting this annotation from user input to the UI
// 2. We fix the plugin to operate migration specific behavior on the
// migrateAnnnotationKey
func (r *MigMigrationReconciler) getAnnotations(migration *migrationsv1alpha1.MigMigration) map[string]string {
	annotations := make(map[string]string)
	if migration.Spec.Stage {
		annotations[migrationsv1alpha1.StageOrFinalMigrationAnnotation] = migrationsv1alpha1.StageMigration
	} else {
		annotations[migrationsv1alpha1.StageOrFinalMigrationAnnotation] = migrationsv1alpha1.FinalMigration
	}
	return annotations
}
