// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdmember

import (
	"fmt"

	druidapicommon "github.com/gardener/etcd-druid/api/common"
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/common"
	"github.com/gardener/etcd-druid/internal/component"
	druiderr "github.com/gardener/etcd-druid/internal/errors"
	"github.com/gardener/etcd-druid/internal/utils"

	"github.com/hashicorp/go-multierror"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// ErrListEtcdMember indicates an error in listing the EtcdMember resources.
	ErrListEtcdMember druidapicommon.ErrorCode = "ERR_LIST_ETCD_MEMBER"
	// ErrSyncEtcdMember indicates an error in syncing the EtcdMember resources.
	ErrSyncEtcdMember druidapicommon.ErrorCode = "ERR_SYNC_ETCD_MEMBER"
	// ErrDeleteEtcdMember indicates an error in deleting the EtcdMember resources.
	ErrDeleteEtcdMember druidapicommon.ErrorCode = "ERR_DELETE_ETCD_MEMBER"
)

type _resource struct {
	client client.Client
}

// New returns a new EtcdMember component operator.
func New(client client.Client) component.Operator {
	return &_resource{
		client: client,
	}
}

// GetExistingResourceNames returns the names of existing EtcdMember resources owned by the given Etcd.
func (r _resource) GetExistingResourceNames(ctx component.OperatorContext, etcdObjMeta metav1.ObjectMeta) ([]string, error) {
	resourceNames := make([]string, 0, 1)

	objMetaList := &metav1.PartialObjectMetadataList{}
	objMetaList.SetGroupVersionKind(druidv1alpha1.SchemeGroupVersion.WithKind("EtcdMember"))
	if err := r.client.List(ctx,
		objMetaList,
		client.InNamespace(etcdObjMeta.Namespace),
		client.MatchingLabels(getSelectorLabelsForAllEtcdMembers(etcdObjMeta)),
	); err != nil {
		return resourceNames, druiderr.WrapError(err,
			ErrListEtcdMember,
			component.OperationGetExistingResourceNames,
			fmt.Sprintf("Error listing EtcdMember resources for etcd: %v", druidv1alpha1.GetNamespaceName(etcdObjMeta)))
	}
	for _, item := range objMetaList.Items {
		if metav1.IsControlledBy(&item, &etcdObjMeta) {
			resourceNames = append(resourceNames, item.Name)
		}
	}
	return resourceNames, nil
}

// PreSync is a no-op for the EtcdMember component.
func (r _resource) PreSync(_ component.OperatorContext, _ *druidv1alpha1.Etcd) error { return nil }

// Sync creates or updates EtcdMember resources for the given Etcd.
// It creates one EtcdMember per replica (index 0 to spec.replicas-1).
// For scale-up, newly created members get the druid.gardener.cloud/create-as-learner annotation.
// For scale-down, EtcdMember resources with index >= replicas are deleted.
func (r _resource) Sync(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) error {
	// List existing EtcdMember resources for this Etcd.
	existingMembers, err := r.listExistingEtcdMembers(ctx, etcd)
	if err != nil {
		return druiderr.WrapError(err,
			ErrSyncEtcdMember,
			component.OperationSync,
			fmt.Sprintf("Error listing existing EtcdMember resources during sync for etcd: %v", druidv1alpha1.GetNamespaceName(etcd.ObjectMeta)))
	}
	existingMemberNames := make(map[string]struct{}, len(existingMembers))
	for _, m := range existingMembers {
		existingMemberNames[m.Name] = struct{}{}
	}

	desiredReplicas := int(etcd.Spec.Replicas)

	// Create or update EtcdMembers for each desired replica.
	createTasks := make([]utils.OperatorTask, 0, desiredReplicas)
	for i := range desiredReplicas {
		memberName := druidv1alpha1.GetEtcdMemberName(etcd.ObjectMeta, i)
		_, exists := existingMemberNames[memberName]
		isScaleUp := !exists && len(existingMembers) > 0
		createTasks = append(createTasks, utils.OperatorTask{
			Name: "CreateOrUpdate-" + memberName,
			Fn: func(ctx component.OperatorContext) error {
				return r.doCreateOrUpdate(ctx, etcd, memberName, isScaleUp)
			},
		})
	}

	var errs error
	if errorList := utils.RunConcurrently(ctx, createTasks); len(errorList) > 0 {
		for _, e := range errorList {
			errs = multierror.Append(errs, e)
		}
	}

	// Delete EtcdMembers that are no longer needed (scale-down).
	for _, member := range existingMembers {
		// Check if this member has an index >= desiredReplicas (i.e., it should be deleted).
		shouldDelete := true
		for i := range desiredReplicas {
			if member.Name == druidv1alpha1.GetEtcdMemberName(etcd.ObjectMeta, i) {
				shouldDelete = false
				break
			}
		}
		if shouldDelete {
			memberToDelete := member
			if err := r.client.Delete(ctx, &memberToDelete); err != nil {
				errs = multierror.Append(errs, druiderr.WrapError(err,
					ErrDeleteEtcdMember,
					component.OperationSync,
					fmt.Sprintf("Error deleting EtcdMember %s during scale-down for etcd: %v", memberToDelete.Name, druidv1alpha1.GetNamespaceName(etcd.ObjectMeta))))
			} else {
				ctx.Logger.Info("deleted EtcdMember during scale-down", "etcdMember", memberToDelete.Name)
			}
		}
	}

	return errs
}

// TriggerDelete deletes all EtcdMember resources owned by the given Etcd.
func (r _resource) TriggerDelete(ctx component.OperatorContext, etcdObjMeta metav1.ObjectMeta) error {
	ctx.Logger.Info("Triggering deletion of EtcdMember resources")
	if err := r.client.DeleteAllOf(ctx,
		&druidv1alpha1.EtcdMember{},
		client.InNamespace(etcdObjMeta.Namespace),
		client.MatchingLabels(getSelectorLabelsForAllEtcdMembers(etcdObjMeta))); err != nil {
		return druiderr.WrapError(err,
			ErrDeleteEtcdMember,
			component.OperationTriggerDelete,
			fmt.Sprintf("Failed to delete EtcdMember resources for etcd: %v", druidv1alpha1.GetNamespaceName(etcdObjMeta)))
	}
	ctx.Logger.Info("deleted", "component", "etcd-members")
	return nil
}

// listExistingEtcdMembers lists all EtcdMember resources owned by this Etcd.
func (r _resource) listExistingEtcdMembers(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) ([]druidv1alpha1.EtcdMember, error) {
	memberList := &druidv1alpha1.EtcdMemberList{}
	if err := r.client.List(ctx,
		memberList,
		client.InNamespace(etcd.Namespace),
		client.MatchingLabels(getSelectorLabelsForAllEtcdMembers(etcd.ObjectMeta)),
	); err != nil {
		return nil, err
	}
	owned := make([]druidv1alpha1.EtcdMember, 0, len(memberList.Items))
	for _, m := range memberList.Items {
		if metav1.IsControlledBy(&m, etcd) {
			owned = append(owned, m)
		}
	}
	return owned, nil
}

func (r _resource) doCreateOrUpdate(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, memberName string, isScaleUp bool) error {
	member := emptyEtcdMember(client.ObjectKey{Name: memberName, Namespace: etcd.Namespace})
	opResult, err := controllerutil.CreateOrPatch(ctx, r.client, member, func() error {
		buildResource(etcd, member, memberName, isScaleUp)
		return nil
	})
	if err != nil {
		return druiderr.WrapError(err,
			ErrSyncEtcdMember,
			component.OperationSync,
			fmt.Sprintf("Error syncing EtcdMember: %s for etcd: %v", memberName, druidv1alpha1.GetNamespaceName(etcd.ObjectMeta)))
	}
	ctx.Logger.Info("triggered create or update of EtcdMember", "etcdMember", memberName, "operationResult", opResult)
	return nil
}

func buildResource(etcd *druidv1alpha1.Etcd, member *druidv1alpha1.EtcdMember, memberName string, isScaleUp bool) {
	member.Labels = getLabels(etcd, memberName)
	member.OwnerReferences = []metav1.OwnerReference{druidv1alpha1.GetAsOwnerReference(etcd.ObjectMeta)}
	// Add create-as-learner annotation for new members during scale-up.
	// Only set the annotation on new resources (those without an existing ResourceVersion)
	// and only when this is a scale-up (not initial cluster creation).
	if isScaleUp && member.ResourceVersion == "" {
		if member.Annotations == nil {
			member.Annotations = make(map[string]string)
		}
		member.Annotations[druidv1alpha1.AnnotationCreateAsLearner] = "true"
	}
}

func getSelectorLabelsForAllEtcdMembers(etcdObjMeta metav1.ObjectMeta) map[string]string {
	matchingLabels := map[string]string{
		druidv1alpha1.LabelComponentKey: common.ComponentNameEtcdMember,
	}
	return utils.MergeMaps(druidv1alpha1.GetDefaultLabels(etcdObjMeta), matchingLabels)
}

func getLabels(etcd *druidv1alpha1.Etcd, memberName string) map[string]string {
	memberLabels := map[string]string{
		druidv1alpha1.LabelComponentKey: common.ComponentNameEtcdMember,
		druidv1alpha1.LabelAppNameKey:   memberName,
	}
	return utils.MergeMaps(memberLabels, druidv1alpha1.GetDefaultLabels(etcd.ObjectMeta))
}

func emptyEtcdMember(objectKey client.ObjectKey) *druidv1alpha1.EtcdMember {
	return &druidv1alpha1.EtcdMember{
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectKey.Name,
			Namespace: objectKey.Namespace,
		},
	}
}
