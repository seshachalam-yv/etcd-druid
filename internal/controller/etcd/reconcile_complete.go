// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcd

import (
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/component"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *Reconciler) completeReconcile(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) ctrlutils.ReconcileStepResult {
	rLog := r.logger.WithValues("etcd", client.ObjectKeyFromObject(etcd), "operation", "completeReconcile").WithValues("runID", ctx.RunID)
	ctx.SetLogger(rLog)

	reconcileCompletionStepFns := []reconcileFn{
		r.clearScaleOperationCondition,
		r.updateObservedGeneration,
		r.removeOperationAnnotation,
	}

	for _, fn := range reconcileCompletionStepFns {
		if stepResult := fn(ctx, etcd); ctrlutils.ShortCircuitReconcileFlow(stepResult) {
			return r.recordIncompleteReconcileOperation(ctx, etcd, stepResult)
		}
	}
	ctx.Logger.Info("Finished reconciliation completion flow")
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) updateObservedGeneration(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) ctrlutils.ReconcileStepResult {
	originalEtcd := etcd.DeepCopy()
	etcd.Status.ObservedGeneration = &etcd.Generation
	if err := r.client.Status().Patch(ctx, etcd, client.MergeFrom(originalEtcd)); err != nil {
		ctx.Logger.Error(err, "failed to patch status.ObservedGeneration")
		return ctrlutils.ReconcileWithError(err)
	}
	ctx.Logger.Info("patched status.ObservedGeneration", "ObservedGeneration", etcd.Generation)
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) removeOperationAnnotation(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) ctrlutils.ReconcileStepResult {
	if druidv1alpha1.HasReconcileOperationAnnotation(etcd.ObjectMeta) {
		ctx.Logger.Info("Removing operation annotation")
		withOpAnnotation := etcd.DeepCopy()
		druidv1alpha1.RemoveOperationAnnotation(etcd.ObjectMeta)
		if err := r.client.Patch(ctx, etcd, client.MergeFrom(withOpAnnotation)); err != nil {
			ctx.Logger.Error(err, "failed to remove operation annotation")
			return ctrlutils.ReconcileWithError(err)
		}
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) clearScaleOperationCondition(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) ctrlutils.ReconcileStepResult {
	for i, c := range etcd.Status.Conditions {
		if c.Type == druidv1alpha1.ConditionTypeScaleOperationInProgress && c.Status == druidv1alpha1.ConditionTrue {
			originalEtcd := etcd.DeepCopy()
			now := metav1.Now()
			etcd.Status.Conditions[i].Status = druidv1alpha1.ConditionFalse
			etcd.Status.Conditions[i].Reason = "ScaleOperationCompleted"
			etcd.Status.Conditions[i].Message = "Scale operation has completed successfully"
			etcd.Status.Conditions[i].LastTransitionTime = now
			etcd.Status.Conditions[i].LastUpdateTime = now
			if err := r.client.Status().Patch(ctx, etcd, client.MergeFrom(originalEtcd)); err != nil {
				ctx.Logger.Error(err, "failed to clear ScaleOperationInProgress condition")
				return ctrlutils.ReconcileWithError(err)
			}
			ctx.Logger.Info("Cleared ScaleOperationInProgress condition")
			break
		}
	}
	return ctrlutils.ContinueReconcile()
}
