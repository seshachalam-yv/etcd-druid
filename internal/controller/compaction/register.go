// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package compaction

import (
	"reflect"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const controllerName = "compaction-controller"

// RegisterWithManager registers the Compaction Controller with the given controller manager.
func (r *Reconciler) RegisterWithManager(mgr ctrl.Manager) error {
	return ctrl.
		NewControllerManagedBy(mgr).
		Named(controllerName).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: *r.config.ConcurrentSyncs,
		}).
		For(&druidv1alpha1.Etcd{}).
		WithEventFilter(compactionJobStatusChanged()).
		Watches(
			&druidv1alpha1.EtcdMember{},
			handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &druidv1alpha1.Etcd{}),
			builder.WithPredicates(etcdMemberSnapshotsChanged()),
		).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// etcdMemberSnapshotsChanged is a predicate that returns true when an EtcdMember's Status.Snapshots section changes.
func etcdMemberSnapshotsChanged() predicate.Predicate {
	snapshotsChanged := func(objOld, objNew client.Object) bool {
		memberOld, ok := objOld.(*druidv1alpha1.EtcdMember)
		if !ok {
			return false
		}
		memberNew, ok := objNew.(*druidv1alpha1.EtcdMember)
		if !ok {
			return false
		}
		return !reflect.DeepEqual(memberOld.Status.Snapshots, memberNew.Status.Snapshots)
	}

	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return snapshotsChanged(e.ObjectOld, e.ObjectNew)
		},
		GenericFunc: func(_ event.GenericEvent) bool {
			return false
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return false
		},
	}
}

// compactionJobStatusChanged is a predicate that is `true` if the status of a compaction job changes.
func compactionJobStatusChanged() predicate.Predicate {
	equalInt32Ptr := func(a, b *int32) bool {
		if a == nil && b == nil {
			return true
		}
		if a == nil || b == nil {
			return false
		}
		return *a == *b
	}

	isCompactionJob := func(obj client.Object) bool {
		job, ok := obj.(*batchv1.Job)
		if !ok {
			return false
		}

		etcdKind := druidv1alpha1.SchemeGroupVersion.WithKind("Etcd").Kind
		// Extract etcd name from the job's owner reference
		for _, ownerRef := range job.OwnerReferences {
			if ownerRef.Kind == etcdKind && ownerRef.APIVersion == druidv1alpha1.SchemeGroupVersion.String() {
				etcdObjMeta := metav1.ObjectMeta{
					Name:      ownerRef.Name,
					Namespace: job.Namespace,
				}
				expectedCompactionJobName := druidv1alpha1.GetCompactionJobName(etcdObjMeta)
				return job.Name == expectedCompactionJobName
			}
		}
		return false
	}

	// statusChange compares only the critical JobStatus fields that should trigger reconciliation.
	// It checks Active, Succeeded, Failed, Terminating, and Ready fields.
	// Returns false if the new job has active pods (Status.Active > 0) to prevent reconciliation during active compaction job execution.
	statusChange := func(objOld, objNew client.Object) bool {
		jobOld, ok := objOld.(*batchv1.Job)
		if !ok {
			return false
		}
		jobNew, ok := objNew.(*batchv1.Job)
		if !ok {
			return false
		}

		oldStatus := jobOld.Status
		newStatus := jobNew.Status

		// Prevent reconciliation when the job has active pods
		if newStatus.Active > 0 {
			return false
		}

		// Compare only the critical status fields
		return oldStatus.Active != newStatus.Active ||
			oldStatus.Succeeded != newStatus.Succeeded ||
			oldStatus.Failed != newStatus.Failed ||
			!equalInt32Ptr(oldStatus.Terminating, newStatus.Terminating) ||
			!equalInt32Ptr(oldStatus.Ready, newStatus.Ready)
	}

	return predicate.Funcs{
		CreateFunc: func(_ event.CreateEvent) bool {
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isCompactionJob(e.ObjectNew) && statusChange(e.ObjectOld, e.ObjectNew)
		},
		GenericFunc: func(_ event.GenericEvent) bool {
			return false
		},
		DeleteFunc: func(_ event.DeleteEvent) bool {
			return false
		},
	}
}
