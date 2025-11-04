/*
Copyright 2021 Red Hat Inc.

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

// PhaseDescriptions are human readable strings that describe a phase
var PhaseDescriptions = map[string]string{
	Created:                                "Migration created.",
	Started:                                "Migration started.",
	StartRefresh:                           "Starting refresh on MigPlan, MigStorage and MigCluster resources",
	WaitForRefresh:                         "Waiting for refresh of MigPlan, MigStorage and MigCluster resources to complete",
	CleanStaleAnnotations:                  "Removing leftover migration annotations and labels from PVs, PVCs, Pods, Namespaces, and VirtualMachines.",
	WaitForRegistriesReady:                 "Waiting for migration registries on source and target clusters to become healthy.",
	AnnotateResources:                      "Adding migration annotations and labels to PVs, PVCs, Pods, Namespaces, and VirtualMachines.",
	QuiesceSourceVM:                        "Quiescing (Scaling to 0 replicas): VirtualMachines in source namespace.",
	EnsureSrcQuiesced:                      "Waiting for Quiesce (Scaling to 0 replicas) to finish for VirtualMachines in source namespace.",
	Verification:                           "Verifying health of migrated Pods.",
	CreateDirectVolumeMigration:            "Creating Direct Volume Migration for cutover",
	WaitForDirectVolumeMigrationToComplete: "Waiting for Direct Volume Migration to complete.",
	EnsureAnnotationsDeleted:               "Removing migration annotations and labels from PVs, PVCs, Pods, Namespaces, and VirtualMachines.",
	MigrationFailed:                        "Migration failed.",
	Canceling:                              "Migration cancellation in progress.",
	Canceled:                               "Migration canceled.",
	Completed:                              "Migration completed.",
}
