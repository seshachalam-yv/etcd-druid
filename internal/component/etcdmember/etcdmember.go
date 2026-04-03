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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// GetExistingResourceNames returns the names of the existing EtcdMember resources for the given Etcd.
func (r _resource) GetExistingResourceNames(ctx component.OperatorContext, etcdObjMeta metav1.ObjectMeta) ([]string, error) {
	resourceNames := make([]string, 0, 1)

	objMetaList := &metav1.PartialObjectMetadataList{}
	objMetaList.SetGroupVersionKind(druidv1alpha1.SchemeGroupVersion.WithKind("EtcdMemberList"))
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
	for _, member := range objMetaList.Items {
		if metav1.IsControlledBy(&member, &etcdObjMeta) {
			resourceNames = append(resourceNames, member.Name)
		}
	}
	return resourceNames, nil
}

// PreSync is a no-op for the EtcdMember component.
func (r _resource) PreSync(_ component.OperatorContext, _ *druidv1alpha1.Etcd) error { return nil }

// Sync creates EtcdMember resources for all replicas that do not yet exist.
// Existing EtcdMember resources are left untouched (status is owned by etcd-steward).
func (r _resource) Sync(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) error {
	existingMembers, err := r.listExistingEtcdMembers(ctx, etcd)
	if err != nil {
		return err
	}
	existingMemberNames := make(map[string]struct{}, len(existingMembers))
	for _, m := range existingMembers {
		existingMemberNames[m.Name] = struct{}{}
	}
	existingCount := len(existingMembers)

	desiredReplicas := int(etcd.Spec.Replicas)
	for i := 0; i < desiredReplicas; i++ {
		memberName := fmt.Sprintf("%s-%d", etcd.Name, i)
		if _, exists := existingMemberNames[memberName]; exists {
			if err := r.ensureOwnerReference(ctx, etcd, memberName); err != nil {
				return err
			}
			continue
		}
		isScaleUp := existingCount > 0
		if err := r.createEtcdMember(ctx, etcd, memberName, isScaleUp); err != nil {
			return err
		}
	}
	return nil
}

// TriggerDelete deletes all EtcdMember resources for the given Etcd.
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

func (r _resource) listExistingEtcdMembers(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd) ([]druidv1alpha1.EtcdMember, error) {
	memberList := &druidv1alpha1.EtcdMemberList{}
	if err := r.client.List(ctx,
		memberList,
		client.InNamespace(etcd.Namespace),
		client.MatchingLabels(getSelectorLabelsForAllEtcdMembers(etcd.ObjectMeta)),
	); err != nil {
		return nil, druiderr.WrapError(err,
			ErrListEtcdMember,
			component.OperationSync,
			fmt.Sprintf("Error listing EtcdMember resources for etcd: %v", druidv1alpha1.GetNamespaceName(etcd.ObjectMeta)))
	}

	var members []druidv1alpha1.EtcdMember
	for _, m := range memberList.Items {
		if metav1.IsControlledBy(&m, etcd) {
			members = append(members, m)
		}
	}
	return members, nil
}

func (r _resource) createEtcdMember(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, memberName string, isScaleUp bool) error {
	member := &druidv1alpha1.EtcdMember{
		ObjectMeta: metav1.ObjectMeta{
			Name:            memberName,
			Namespace:       etcd.Namespace,
			Labels:          getLabels(etcd, memberName),
			OwnerReferences: []metav1.OwnerReference{druidv1alpha1.GetAsOwnerReference(etcd.ObjectMeta)},
		},
	}
	if isScaleUp {
		member.Annotations = map[string]string{
			druidv1alpha1.CreateAsLearnerAnnotation: "true",
		}
	}
	if err := r.client.Create(ctx, member); err != nil {
		return druiderr.WrapError(err,
			ErrSyncEtcdMember,
			component.OperationSync,
			fmt.Sprintf("Error creating EtcdMember: %s for etcd: %v", memberName, druidv1alpha1.GetNamespaceName(etcd.ObjectMeta)))
	}
	ctx.Logger.Info("created EtcdMember", "name", memberName, "isScaleUp", isScaleUp)
	return nil
}

func (r _resource) ensureOwnerReference(ctx component.OperatorContext, etcd *druidv1alpha1.Etcd, memberName string) error {
	member := &druidv1alpha1.EtcdMember{}
	objKey := client.ObjectKey{Name: memberName, Namespace: etcd.Namespace}
	if err := r.client.Get(ctx, objKey, member); err != nil {
		return druiderr.WrapError(err,
			ErrSyncEtcdMember,
			component.OperationSync,
			fmt.Sprintf("Error getting EtcdMember: %s for etcd: %v", memberName, druidv1alpha1.GetNamespaceName(etcd.ObjectMeta)))
	}

	if metav1.IsControlledBy(member, etcd) {
		return nil
	}

	member.OwnerReferences = []metav1.OwnerReference{druidv1alpha1.GetAsOwnerReference(etcd.ObjectMeta)}
	if err := r.client.Update(ctx, member); err != nil {
		return druiderr.WrapError(err,
			ErrSyncEtcdMember,
			component.OperationSync,
			fmt.Sprintf("Error updating OwnerReference on EtcdMember: %s for etcd: %v", memberName, druidv1alpha1.GetNamespaceName(etcd.ObjectMeta)))
	}
	return nil
}

func getSelectorLabelsForAllEtcdMembers(etcdObjMeta metav1.ObjectMeta) map[string]string {
	memberMatchingLabels := map[string]string{
		druidv1alpha1.LabelComponentKey: common.ComponentNameEtcdMember,
	}
	return utils.MergeMaps(druidv1alpha1.GetDefaultLabels(etcdObjMeta), memberMatchingLabels)
}

func getLabels(etcd *druidv1alpha1.Etcd, memberName string) map[string]string {
	memberLabels := map[string]string{
		druidv1alpha1.LabelComponentKey: common.ComponentNameEtcdMember,
		druidv1alpha1.LabelAppNameKey:   memberName,
		druidv1alpha1.LabelOwnedByKey:   etcd.Name,
	}
	return utils.MergeMaps(memberLabels, druidv1alpha1.GetDefaultLabels(etcd.ObjectMeta))
}
