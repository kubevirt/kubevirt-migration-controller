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

package storagemig

import (
	"context"
	"time"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
)

// Perform the migration.
func (r *StorageMigrationReconciler) migrate(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan, migration *migrations.VirtualMachineStorageMigration) (time.Duration, error) {
	log := r.Log
	// Run
	task := Task{
		Client: r.Client,
		Owner:  migration,
		Plan:   plan,
		Log:    log,
		Config: r.Config,
	}

	log.V(5).Info("Calling task.Run")
	if err := task.Run(ctx); err != nil {
		log.Error(err, "Task.Run failed")
		return task.Requeue, err
	}

	return task.Requeue, nil
}
