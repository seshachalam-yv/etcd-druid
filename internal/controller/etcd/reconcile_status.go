// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcd

import (
	druidconfigv1alpha1 "github.com/gardener/etcd-druid/api/config/v1alpha1"
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/component"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"
	"github.com/gardener/etcd-druid/internal/health/status"
	"github.com/gardener/etcd-druid/internal/utils/kubernetes"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// mutateEtcdStatusFn is a function which mutates the status of the passed etcd object
type mutateEtcdStatusFn func(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, logger logr.Logger) ctrlutils.ReconcileStepResult

func (r *Reconciler) reconcileStatus(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) ctrlutils.ReconcileStepResult {
	sLog := r.logger.WithValues("etcd", client.ObjectKeyFromObject(etcd), "operation", "reconcileStatus").WithValues("runID", ctx.RunID)
	if !druidv1alpha1.IsEtcdRuntimeComponentCreationEnabled(etcd.ObjectMeta) {
		sLog.Info("Skipping status checks since etcd runtime component creation is disabled")
		return ctrlutils.ContinueReconcile()
	}
	originalEtcd := etcd.DeepCopy()

	var mutateETCDStatusStepFns = []mutateEtcdStatusFn{
		r.mutateETCDStatusWithMemberStatusAndConditions,
		r.inspectStatefulSetAndMutateETCDStatus,
		r.setSelector,
	}

	for _, fn := range mutateETCDStatusStepFns {
		if stepResult := fn(ctx, etcd, sLog); ctrlutils.ShortCircuitReconcileFlow(stepResult) {
			return stepResult
		}
	}
	if err := r.client.Status().Patch(ctx, etcd, client.MergeFrom(originalEtcd)); err != nil {
		sLog.Error(err, "failed to update etcd status")
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) mutateETCDStatusWithMemberStatusAndConditions(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, logger logr.Logger) ctrlutils.ReconcileStepResult {
	if druidconfigv1alpha1.DefaultFeatureGates.IsEnabled(druidconfigv1alpha1.UseEtcdSteward) {
		return r.mutateETCDStatusFromEtcdMembers(ctx, etcd, logger)
	}
	statusCheck := status.NewChecker(r.client, r.config.EtcdMember.NotReadyThreshold.Duration, r.config.EtcdMember.UnknownThreshold.Duration)
	if err := statusCheck.Check(ctx, logger, etcd); err != nil {
		logger.Error(err, "Error executing status checks to update member status and conditions")
		return ctrlutils.ReconcileWithError(err)
	}
	return ctrlutils.ContinueReconcile()
}

// mutateETCDStatusFromEtcdMembers populates Etcd.Status.Members from EtcdMember custom resources
// instead of member leases. This is used when UseEtcdSteward feature gate is enabled.
func (r *Reconciler) mutateETCDStatusFromEtcdMembers(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, logger logr.Logger) ctrlutils.ReconcileStepResult {
	memberList := &druidv1alpha1.EtcdMemberList{}
	if err := r.client.List(ctx, memberList,
		client.InNamespace(etcd.Namespace),
		client.MatchingLabels(druidv1alpha1.GetDefaultLabels(etcd.ObjectMeta)),
	); err != nil {
		logger.Error(err, "Error listing EtcdMember resources for status update")
		return ctrlutils.ReconcileWithError(err)
	}

	members := make([]druidv1alpha1.EtcdMemberStatus, 0, len(memberList.Items))
	for _, em := range memberList.Items {
		if !metav1.IsControlledBy(&em, etcd) {
			continue
		}
		memberStatus := druidv1alpha1.EtcdMemberStatus{
			Name: em.Name,
			ID:   em.Status.ID,
		}

		// Map EtcdMember state/substate to EtcdMemberConditionStatus and Role.
		if em.Status.State != nil {
			switch *em.Status.State {
			case druidv1alpha1.MemberStateStarted:
				memberStatus.Status = druidv1alpha1.EtcdMemberStatusReady
				memberStatus.Reason = "MemberStarted"
				if em.Status.SubState != nil {
					switch *em.Status.SubState {
					case druidv1alpha1.MemberSubStateLeader:
						role := druidv1alpha1.EtcdRoleLeader
						memberStatus.Role = &role
					case druidv1alpha1.MemberSubStateFollower:
						role := druidv1alpha1.EtcdRoleMember
						memberStatus.Role = &role
					}
				}
			case druidv1alpha1.MemberStateStarting:
				memberStatus.Status = druidv1alpha1.EtcdMemberStatusNotReady
				memberStatus.Reason = "MemberStarting"
			case druidv1alpha1.MemberStateInitializing:
				memberStatus.Status = druidv1alpha1.EtcdMemberStatusNotReady
				memberStatus.Reason = "MemberInitializing"
			case druidv1alpha1.MemberStateNew:
				memberStatus.Status = druidv1alpha1.EtcdMemberStatusUnknown
				memberStatus.Reason = "MemberNew"
			default:
				memberStatus.Status = druidv1alpha1.EtcdMemberStatusUnknown
				memberStatus.Reason = "UnknownState"
			}
		} else {
			memberStatus.Status = druidv1alpha1.EtcdMemberStatusUnknown
			memberStatus.Reason = "StateNotReported"
		}

		// Preserve LastTransitionTime from old status if status didn't change
		for _, oldMember := range etcd.Status.Members {
			if oldMember.Name == memberStatus.Name && oldMember.Status == memberStatus.Status {
				memberStatus.LastTransitionTime = oldMember.LastTransitionTime
				break
			}
		}
		if memberStatus.LastTransitionTime.IsZero() {
			memberStatus.LastTransitionTime = metav1.Now()
		}

		members = append(members, memberStatus)
	}
	etcd.Status.Members = members

	// Still run condition checks using the legacy checker, which reads the now-populated etcd.Status.Members.
	statusCheck := status.NewChecker(r.client, r.config.EtcdMember.NotReadyThreshold.Duration, r.config.EtcdMember.UnknownThreshold.Duration)
	if err := statusCheck.ExecuteConditionChecks(ctx, etcd); err != nil {
		logger.Error(err, "Error executing condition checks")
		return ctrlutils.ReconcileWithError(err)
	}

	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) inspectStatefulSetAndMutateETCDStatus(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, _ logr.Logger) ctrlutils.ReconcileStepResult {
	sts, err := kubernetes.GetStatefulSet(ctx, r.client, etcd)
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	if sts != nil {
		etcd.Status.Etcd = &druidv1alpha1.CrossVersionObjectReference{
			APIVersion: sts.APIVersion,
			Kind:       sts.Kind,
			Name:       sts.Name,
		}
		expectedReplicas := etcd.Spec.Replicas
		// if the latest Etcd spec has not yet been reconciled by druid, then check sts readiness against sts.spec.replicas instead
		if etcd.Status.ObservedGeneration == nil || *etcd.Status.ObservedGeneration != etcd.Generation {
			expectedReplicas = *sts.Spec.Replicas
		}
		ready, _ := kubernetes.IsStatefulSetReady(expectedReplicas, sts)
		etcd.Status.CurrentReplicas = sts.Status.CurrentReplicas
		etcd.Status.ReadyReplicas = sts.Status.ReadyReplicas
		etcd.Status.Replicas = sts.Status.CurrentReplicas
		etcd.Status.Ready = &ready
	} else {
		etcd.Status.CurrentReplicas = 0
		etcd.Status.ReadyReplicas = 0
		etcd.Status.Ready = ptr.To(false)
	}
	return ctrlutils.ContinueReconcile()
}

func (r *Reconciler) setSelector(_ component.OperatorContext, etcd *druidv1alpha1.Etcd, _ logr.Logger) ctrlutils.ReconcileStepResult {
	labels := druidv1alpha1.GetDefaultLabels(etcd.ObjectMeta)
	selector, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: labels})
	if err != nil {
		return ctrlutils.ReconcileWithError(err)
	}
	etcd.Status.Selector = ptr.To(selector.String())
	return ctrlutils.ContinueReconcile()
}
