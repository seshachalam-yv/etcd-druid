// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcd

import (
	"fmt"
	"strings"
	"time"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/component"
	ctrlutils "github.com/gardener/etcd-druid/internal/controller/utils"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// preSyncScaleDownRequeueInterval is the interval after which the reconciler will requeue
	// when waiting for a RemoveMembers OpsTask to complete.
	preSyncScaleDownRequeueInterval = 10 * time.Second
	// removeMembersOpsTaskTTL is the TTL in seconds for completed RemoveMembers OpsTask resources.
	removeMembersOpsTaskTTL int32 = 300
	// maxOpsTaskNameLength is the maximum allowed length for a Kubernetes resource name.
	maxOpsTaskNameLength = 63
)

// reconcilePreSyncScaleDown detects members that should be removed from an existing cluster
// that this etcd joined (LiveCPM scenario) and creates an EtcdOpsTask to remove them.
// It should be called BEFORE syncEtcdResources in the reconcile flow.
func (r *Reconciler) reconcilePreSyncScaleDown(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) ctrlutils.ReconcileStepResult {
	rLog := r.logger.WithValues("etcd", client.ObjectKeyFromObject(etcd), "operation", "reconcilePreSyncScaleDown").WithValues("runID", ctx.RunID)
	ctx.SetLogger(rLog)

	rLog.Info("Running PreSync scale-down check",
		"specBootstrapNil", etcd.Spec.Etcd.BootstrapWithExistingCluster == nil,
		"statusBootstrapNil", etcd.Status.BootstrapWithExistingClusterMembers == nil)

	membersToRemove := computeMembersToRemove(etcd)
	if len(membersToRemove) == 0 {
		rLog.Info("No members to remove")
		return ctrlutils.ContinueReconcile()
	}

	rLog.Info("Detected members to remove from source cluster", "count", len(membersToRemove))

	opsTaskName := getRemoveMembersOpsTaskName(etcd.Name)

	// Check for an existing EtcdOpsTask
	existingTask := &druidv1alpha1.EtcdOpsTask{}
	taskKey := client.ObjectKey{Namespace: etcd.Namespace, Name: opsTaskName}
	err := r.client.Get(ctx, taskKey, existingTask)

	if err == nil {
		// Task exists - check its state
		return r.handleExistingRemoveMembersTask(ctx, etcd, existingTask, membersToRemove)
	}

	if !apierrors.IsNotFound(err) {
		rLog.Error(err, "Failed to get existing RemoveMembers EtcdOpsTask")
		return ctrlutils.ReconcileWithError(err)
	}

	// No existing task - create one
	return r.createRemoveMembersOpsTask(ctx, etcd, opsTaskName, membersToRemove)
}

// computeMembersToRemove determines which members from the source cluster should be removed.
// Detection logic:
// - If spec.etcd.bootstrapWithExistingCluster == nil AND status.bootstrapWithExistingClusterMembers.joinedWith is non-empty:
//   ALL members in joinedWith should be removed
// - If spec.etcd.bootstrapWithExistingCluster != nil AND a member in joinedWith is NOT found (by name) in
//   spec.etcd.bootstrapWithExistingCluster.members: that member should be removed
func computeMembersToRemove(etcd *druidv1alpha1.Etcd) []druidv1alpha1.MemberToRemove {
	// No status tracking means nothing to remove
	if etcd.Status.BootstrapWithExistingClusterMembers == nil {
		return nil
	}
	joinedWith := etcd.Status.BootstrapWithExistingClusterMembers.JoinedWith
	if len(joinedWith) == 0 {
		return nil
	}

	specBootstrap := etcd.Spec.Etcd.BootstrapWithExistingCluster

	if specBootstrap == nil {
		// Spec has been removed entirely - all joined members should be removed
		return buildMembersToRemoveFromJoined(joinedWith)
	}

	// Build a set of member names still in spec
	specMemberNames := make(map[string]struct{}, len(specBootstrap.Members))
	for _, m := range specBootstrap.Members {
		specMemberNames[m.Name] = struct{}{}
	}

	// Find joined members no longer present in spec
	var toRemove []druidv1alpha1.MemberToRemove
	for _, joined := range joinedWith {
		if _, found := specMemberNames[joined.Name]; !found {
			peerURL := getPeerURLForJoinedMember(joined, specBootstrap)
			toRemove = append(toRemove, druidv1alpha1.MemberToRemove{
				Name:    joined.Name,
				PeerURL: peerURL,
			})
		}
	}
	return toRemove
}

// buildMembersToRemoveFromJoined converts all joined members to MemberToRemove entries.
func buildMembersToRemoveFromJoined(joined []druidv1alpha1.BootstrapJoinedMember) []druidv1alpha1.MemberToRemove {
	result := make([]druidv1alpha1.MemberToRemove, 0, len(joined))
	for _, m := range joined {
		peerURL := ""
		if len(m.PeerURLs) > 0 {
			peerURL = m.PeerURLs[0]
		}
		result = append(result, druidv1alpha1.MemberToRemove{
			Name:    m.Name,
			PeerURL: peerURL,
		})
	}
	return result
}

// getPeerURLForJoinedMember gets the peer URL for a joined member.
// It first checks the joined member's stored PeerURLs, then falls back to searching the spec.
func getPeerURLForJoinedMember(joined druidv1alpha1.BootstrapJoinedMember, specBootstrap *druidv1alpha1.BootstrapWithExistingCluster) string {
	// First try from the stored PeerURLs on the joined member status
	if len(joined.PeerURLs) > 0 {
		return joined.PeerURLs[0]
	}
	// Fallback: search the spec members for matching PeerURL
	if specBootstrap != nil {
		for _, specMember := range specBootstrap.Members {
			if specMember.Name == joined.Name && len(specMember.PeerURLs) > 0 {
				return specMember.PeerURLs[0]
			}
		}
	}
	return ""
}

// handleExistingRemoveMembersTask processes the outcome of an existing RemoveMembers EtcdOpsTask.
func (r *Reconciler) handleExistingRemoveMembersTask(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, task *druidv1alpha1.EtcdOpsTask, membersToRemove []druidv1alpha1.MemberToRemove) ctrlutils.ReconcileStepResult {
	if task.Status.State == nil {
		// Task has no state yet (Pending), requeue to wait
		ctx.Logger.Info("RemoveMembers EtcdOpsTask is pending, requeuing")
		return ctrlutils.ReconcileAfter(preSyncScaleDownRequeueInterval, "waiting for RemoveMembers EtcdOpsTask to be processed")
	}

	switch *task.Status.State {
	case druidv1alpha1.TaskStateSucceeded:
		ctx.Logger.Info("RemoveMembers EtcdOpsTask succeeded, cleaning up status")
		return r.cleanupAfterSuccessfulRemoval(ctx, etcd, task, membersToRemove)

	case druidv1alpha1.TaskStateFailed, druidv1alpha1.TaskStateRejected:
		ctx.Logger.Error(fmt.Errorf("RemoveMembers EtcdOpsTask is in state %s", *task.Status.State),
			"RemoveMembers operation failed or was rejected",
			"taskName", task.Name,
			"taskState", *task.Status.State)
		return ctrlutils.ReconcileWithError(fmt.Errorf("RemoveMembers EtcdOpsTask %s is in state %s", task.Name, *task.Status.State))

	default:
		// InProgress or Pending - requeue and wait
		ctx.Logger.Info("RemoveMembers EtcdOpsTask is still in progress, requeuing", "state", *task.Status.State)
		return ctrlutils.ReconcileAfter(preSyncScaleDownRequeueInterval, fmt.Sprintf("waiting for RemoveMembers EtcdOpsTask to complete, current state: %s", *task.Status.State))
	}
}

// cleanupAfterSuccessfulRemoval performs post-removal cleanup:
// 1. Remove successfully-removed members from status.bootstrapWithExistingClusterMembers.joinedWith
// 2. If joinedWith becomes empty: set BootstrapWithExistingCluster condition to False
// 3. Delete the completed OpsTask
// 4. Patch the Etcd status
func (r *Reconciler) cleanupAfterSuccessfulRemoval(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, task *druidv1alpha1.EtcdOpsTask, removedMembers []druidv1alpha1.MemberToRemove) ctrlutils.ReconcileStepResult {
	originalEtcd := etcd.DeepCopy()

	// Build a set of removed member names
	removedNames := make(map[string]struct{}, len(removedMembers))
	for _, m := range removedMembers {
		removedNames[m.Name] = struct{}{}
	}

	// Remove the removed members from joinedWith
	if etcd.Status.BootstrapWithExistingClusterMembers != nil {
		remaining := make([]druidv1alpha1.BootstrapJoinedMember, 0)
		for _, joined := range etcd.Status.BootstrapWithExistingClusterMembers.JoinedWith {
			if _, wasRemoved := removedNames[joined.Name]; !wasRemoved {
				remaining = append(remaining, joined)
			}
		}
		etcd.Status.BootstrapWithExistingClusterMembers.JoinedWith = remaining

		// If joinedWith is now empty, set the condition to False and clear the status
		if len(remaining) == 0 {
			r.setBootstrapConditionFalse(etcd)
			etcd.Status.BootstrapWithExistingClusterMembers = nil
		}
	}

	// Patch the status
	if err := r.client.Status().Patch(ctx, etcd, client.MergeFrom(originalEtcd)); err != nil {
		ctx.Logger.Error(err, "Failed to patch etcd status after member removal cleanup")
		return ctrlutils.ReconcileWithError(err)
	}

	// Delete the completed OpsTask
	if err := r.client.Delete(ctx, task); err != nil && !apierrors.IsNotFound(err) {
		ctx.Logger.Error(err, "Failed to delete completed RemoveMembers EtcdOpsTask", "taskName", task.Name)
		return ctrlutils.ReconcileWithError(err)
	}

	ctx.Logger.Info("Successfully cleaned up after member removal", "removedCount", len(removedMembers))
	return ctrlutils.ContinueReconcile()
}

// setBootstrapConditionFalse sets the BootstrapWithExistingCluster condition to False.
func (r *Reconciler) setBootstrapConditionFalse(etcd *druidv1alpha1.Etcd) {
	now := metav1.Now()
	found := false
	for i, cond := range etcd.Status.Conditions {
		if cond.Type == druidv1alpha1.ConditionTypeBootstrapWithExistingCluster {
			etcd.Status.Conditions[i] = druidv1alpha1.Condition{
				Type:               druidv1alpha1.ConditionTypeBootstrapWithExistingCluster,
				Status:             druidv1alpha1.ConditionFalse,
				LastTransitionTime: now,
				LastUpdateTime:     now,
				Reason:             "BootstrapComplete",
				Message:            "Original members removed. Cluster operating independently.",
			}
			found = true
			break
		}
	}
	if !found {
		etcd.Status.Conditions = append(etcd.Status.Conditions, druidv1alpha1.Condition{
			Type:               druidv1alpha1.ConditionTypeBootstrapWithExistingCluster,
			Status:             druidv1alpha1.ConditionFalse,
			LastTransitionTime: now,
			LastUpdateTime:     now,
			Reason:             "BootstrapComplete",
			Message:            "Original members removed. Cluster operating independently.",
		})
	}
}

// createRemoveMembersOpsTask creates a new EtcdOpsTask to remove the specified members.
func (r *Reconciler) createRemoveMembersOpsTask(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, taskName string, membersToRemove []druidv1alpha1.MemberToRemove) ctrlutils.ReconcileStepResult {
	opsTask := &druidv1alpha1.EtcdOpsTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:            taskName,
			Namespace:       etcd.Namespace,
			OwnerReferences: []metav1.OwnerReference{druidv1alpha1.GetAsOwnerReference(etcd.ObjectMeta)},
		},
		Spec: druidv1alpha1.EtcdOpsTaskSpec{
			EtcdName:                ptr.To(etcd.Name),
			TTLSecondsAfterFinished: ptr.To(removeMembersOpsTaskTTL),
			Config: druidv1alpha1.EtcdOpsTaskConfig{
				RemoveMembers: &druidv1alpha1.RemoveMembersConfig{
					MembersToRemove: membersToRemove,
				},
			},
		},
	}

	// Set controller reference for garbage collection
	if err := controllerutil.SetControllerReference(etcd, opsTask, r.client.Scheme()); err != nil {
		ctx.Logger.Error(err, "Failed to set controller reference on RemoveMembers EtcdOpsTask")
		// Fallback: we already set OwnerReferences directly, proceed without controller ref
	}

	if err := r.client.Create(ctx, opsTask); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Race condition: task was just created by another reconciliation, requeue
			ctx.Logger.Info("RemoveMembers EtcdOpsTask already exists, requeuing")
			return ctrlutils.ReconcileAfter(preSyncScaleDownRequeueInterval, "RemoveMembers EtcdOpsTask already exists")
		}
		ctx.Logger.Error(err, "Failed to create RemoveMembers EtcdOpsTask")
		return ctrlutils.ReconcileWithError(err)
	}

	ctx.Logger.Info("Created RemoveMembers EtcdOpsTask", "taskName", taskName, "membersToRemove", len(membersToRemove))
	return ctrlutils.ReconcileAfter(preSyncScaleDownRequeueInterval, "waiting for RemoveMembers EtcdOpsTask to be processed")
}

// getRemoveMembersOpsTaskName returns the name for the RemoveMembers EtcdOpsTask, truncated to 63 chars.
func getRemoveMembersOpsTaskName(etcdName string) string {
	name := fmt.Sprintf("remove-members-%s", etcdName)
	if len(name) > maxOpsTaskNameLength {
		name = strings.TrimRight(name[:maxOpsTaskNameLength], "-")
	}
	return name
}
