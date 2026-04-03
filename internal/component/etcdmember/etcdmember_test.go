// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdmember

import (
	"context"
	"fmt"
	"testing"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/client/kubernetes"
	"github.com/gardener/etcd-druid/internal/component"
	druiderr "github.com/gardener/etcd-druid/internal/errors"
	testutils "github.com/gardener/etcd-druid/test/utils"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

// ----------------------------------- GetExistingResourceNames -----------------------------------
func TestGetExistingResourceNames(t *testing.T) {
	testCases := []struct {
		name               string
		etcdReplicas       int32
		numExistingMembers int
		expectedErr        bool
	}{
		{
			name:               "all EtcdMembers exist for a 3 node etcd cluster",
			etcdReplicas:       3,
			numExistingMembers: 3,
		},
		{
			name:               "2 of 3 EtcdMembers exist for a 3 node etcd cluster",
			etcdReplicas:       3,
			numExistingMembers: 2,
		},
		{
			name:               "should return an empty slice when no EtcdMembers are found",
			etcdReplicas:       3,
			numExistingMembers: 0,
		},
		{
			name:               "EtcdMember exists for a single node etcd cluster",
			etcdReplicas:       1,
			numExistingMembers: 1,
		},
	}

	g := NewWithT(t)
	t.Parallel()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			etcd := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).WithReplicas(tc.etcdReplicas).Build()
			var existingObjects []client.Object
			if tc.numExistingMembers > 0 {
				members := newEtcdMembers(g, etcd, tc.numExistingMembers, false)
				for _, m := range members {
					existingObjects = append(existingObjects, m)
				}
			}
			cl := testutils.NewTestClientBuilder().
				WithScheme(kubernetes.Scheme).
				WithObjects(existingObjects...).
				Build()
			operator := New(cl)
			opCtx := component.NewOperatorContext(context.Background(), logr.Discard(), uuid.NewString())
			memberNames, err := operator.GetExistingResourceNames(opCtx, etcd.ObjectMeta)
			if tc.expectedErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(memberNames).To(HaveLen(tc.numExistingMembers))
			}
		})
	}
}

// ----------------------------------- Sync -----------------------------------
func TestSyncInitialCreation(t *testing.T) {
	// Initial creation (0->3 replicas): creates 3 EtcdMembers, none have create-as-learner annotation.
	g := NewWithT(t)
	etcd := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).WithReplicas(3).Build()
	cl := testutils.NewTestClientBuilder().
		WithScheme(kubernetes.Scheme).
		Build()
	operator := New(cl)
	opCtx := component.NewOperatorContext(context.Background(), logr.Discard(), uuid.NewString())

	err := operator.Sync(opCtx, etcd)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify 3 EtcdMembers created
	members := getLatestEtcdMembers(g, cl, etcd)
	g.Expect(members).To(HaveLen(3))

	// Verify none have create-as-learner annotation
	for _, m := range members {
		_, hasLearnerAnnotation := m.Annotations[druidv1alpha1.CreateAsLearnerAnnotation]
		g.Expect(hasLearnerAnnotation).To(BeFalse(), "EtcdMember %s should not have create-as-learner annotation on initial creation", m.Name)
	}

	// Verify OwnerReferences
	for _, m := range members {
		g.Expect(metav1.IsControlledBy(&m, etcd)).To(BeTrue(), "EtcdMember %s should be controlled by Etcd", m.Name)
	}

	// Verify labels
	for _, m := range members {
		g.Expect(m.Labels[druidv1alpha1.LabelOwnedByKey]).To(Equal(etcd.Name))
		g.Expect(m.Labels[druidv1alpha1.LabelComponentKey]).To(Equal("etcd-member"))
	}
}

func TestSyncScaleUp(t *testing.T) {
	// Scale-up (1->3 replicas, 1 existing): creates EtcdMembers at indices 1 and 2 WITH create-as-learner annotation.
	// Existing index 0 is NOT modified.
	g := NewWithT(t)
	etcd := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).WithReplicas(3).Build()

	// Create 1 existing EtcdMember (index 0)
	existingMembers := newEtcdMembers(g, etcd, 1, false)
	var existingObjects []client.Object
	for _, m := range existingMembers {
		existingObjects = append(existingObjects, m)
	}

	cl := testutils.NewTestClientBuilder().
		WithScheme(kubernetes.Scheme).
		WithObjects(existingObjects...).
		Build()
	operator := New(cl)
	opCtx := component.NewOperatorContext(context.Background(), logr.Discard(), uuid.NewString())

	err := operator.Sync(opCtx, etcd)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify 3 EtcdMembers exist
	members := getLatestEtcdMembers(g, cl, etcd)
	g.Expect(members).To(HaveLen(3))

	// Find members by name
	memberMap := make(map[string]druidv1alpha1.EtcdMember)
	for _, m := range members {
		memberMap[m.Name] = m
	}

	// Index 0 should NOT have the create-as-learner annotation
	m0, ok := memberMap[fmt.Sprintf("%s-0", etcd.Name)]
	g.Expect(ok).To(BeTrue())
	_, hasLearner0 := m0.Annotations[druidv1alpha1.CreateAsLearnerAnnotation]
	g.Expect(hasLearner0).To(BeFalse(), "Existing member at index 0 should not have create-as-learner annotation")

	// Index 1 should have the create-as-learner annotation
	m1, ok := memberMap[fmt.Sprintf("%s-1", etcd.Name)]
	g.Expect(ok).To(BeTrue())
	g.Expect(m1.Annotations[druidv1alpha1.CreateAsLearnerAnnotation]).To(Equal("true"), "New member at index 1 should have create-as-learner annotation")

	// Index 2 should have the create-as-learner annotation
	m2, ok := memberMap[fmt.Sprintf("%s-2", etcd.Name)]
	g.Expect(ok).To(BeTrue())
	g.Expect(m2.Annotations[druidv1alpha1.CreateAsLearnerAnnotation]).To(Equal("true"), "New member at index 2 should have create-as-learner annotation")
}

func TestSyncIdempotent(t *testing.T) {
	// Sync called twice with same replica count — second call does not recreate or overwrite.
	g := NewWithT(t)
	etcd := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).WithReplicas(3).Build()
	cl := testutils.NewTestClientBuilder().
		WithScheme(kubernetes.Scheme).
		Build()
	operator := New(cl)
	opCtx := component.NewOperatorContext(context.Background(), logr.Discard(), uuid.NewString())

	// First sync
	err := operator.Sync(opCtx, etcd)
	g.Expect(err).ToNot(HaveOccurred())
	membersAfterFirst := getLatestEtcdMembers(g, cl, etcd)
	g.Expect(membersAfterFirst).To(HaveLen(3))

	// Record resource versions from first sync
	rvMap := make(map[string]string)
	for _, m := range membersAfterFirst {
		rvMap[m.Name] = m.ResourceVersion
	}

	// Second sync — should be idempotent
	err = operator.Sync(opCtx, etcd)
	g.Expect(err).ToNot(HaveOccurred())
	membersAfterSecond := getLatestEtcdMembers(g, cl, etcd)
	g.Expect(membersAfterSecond).To(HaveLen(3))

	// Verify resource versions are unchanged (no update occurred)
	for _, m := range membersAfterSecond {
		g.Expect(m.ResourceVersion).To(Equal(rvMap[m.Name]), "EtcdMember %s should not be modified on second sync", m.Name)
	}
}

// ----------------------------------- TriggerDelete -----------------------------------
func TestTriggerDelete(t *testing.T) {
	testCases := []struct {
		name               string
		etcdReplicas       int32
		numExistingMembers int
	}{
		{
			name:               "no-op when no EtcdMember exists",
			etcdReplicas:       3,
			numExistingMembers: 0,
		},
		{
			name:               "successfully deletes all EtcdMembers",
			etcdReplicas:       3,
			numExistingMembers: 3,
		},
		{
			name:               "successfully deletes remaining EtcdMembers",
			etcdReplicas:       3,
			numExistingMembers: 1,
		},
	}

	g := NewWithT(t)
	t.Parallel()

	nonTargetEtcd := testutils.EtcdBuilderWithDefaults("another-etcd", testutils.TestNamespace).Build()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			etcd := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).WithReplicas(tc.etcdReplicas).Build()
			var existingObjects []client.Object
			if tc.numExistingMembers > 0 {
				members := newEtcdMembers(g, etcd, tc.numExistingMembers, false)
				for _, m := range members {
					existingObjects = append(existingObjects, m)
				}
			}
			// Also add non-target EtcdMembers to ensure they're not deleted
			nonTargetMembers := newEtcdMembers(g, nonTargetEtcd, 1, false)
			for _, m := range nonTargetMembers {
				existingObjects = append(existingObjects, m)
			}

			cl := testutils.NewTestClientBuilder().
				WithScheme(kubernetes.Scheme).
				WithObjects(existingObjects...).
				Build()
			operator := New(cl)
			opCtx := component.NewOperatorContext(context.Background(), logr.Discard(), uuid.NewString())

			err := operator.TriggerDelete(opCtx, etcd.ObjectMeta)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify target EtcdMembers are deleted
			membersPostDelete := getLatestEtcdMembers(g, cl, etcd)
			g.Expect(membersPostDelete).To(HaveLen(0))

			// Verify non-target EtcdMembers are NOT deleted
			nonTargetMembersPostDelete := getLatestEtcdMembers(g, cl, nonTargetEtcd)
			g.Expect(nonTargetMembersPostDelete).To(HaveLen(1))
		})
	}
}

// ----------------------------------- TriggerDelete Error -----------------------------------
func TestTriggerDeleteError(t *testing.T) {
	g := NewWithT(t)
	etcd := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).WithReplicas(3).Build()
	members := newEtcdMembers(g, etcd, 3, false)
	var existingObjects []client.Object
	for _, m := range members {
		existingObjects = append(existingObjects, m)
	}

	cl := testutils.NewTestClientBuilder().
		WithScheme(kubernetes.Scheme).
		WithObjects(existingObjects...).
		RecordErrorForObjectsMatchingLabels(testutils.ClientMethodDeleteAll, etcd.Namespace, getSelectorLabelsForAllEtcdMembers(etcd.ObjectMeta), testutils.TestAPIInternalErr).
		Build()
	operator := New(cl)
	opCtx := component.NewOperatorContext(context.Background(), logr.Discard(), uuid.NewString())

	err := operator.TriggerDelete(opCtx, etcd.ObjectMeta)
	g.Expect(err).To(HaveOccurred())
	testutils.CheckDruidError(g, &druiderr.DruidError{
		Code:      ErrDeleteEtcdMember,
		Cause:     testutils.TestAPIInternalErr,
		Operation: component.OperationTriggerDelete,
	}, err)
}

// ---------------------------- Helper Functions -----------------------------

func getLatestEtcdMembers(g *WithT, cl client.Client, etcd *druidv1alpha1.Etcd) []druidv1alpha1.EtcdMember {
	memberList := &druidv1alpha1.EtcdMemberList{}
	g.Expect(cl.List(context.Background(),
		memberList,
		client.InNamespace(etcd.Namespace),
		client.MatchingLabels(getSelectorLabelsForAllEtcdMembers(etcd.ObjectMeta)))).To(Succeed())
	return memberList.Items
}

func newEtcdMembers(g *WithT, etcd *druidv1alpha1.Etcd, numMembers int, withLearnerAnnotation bool) []*druidv1alpha1.EtcdMember {
	g.Expect(numMembers).To(BeNumerically("<=", int(etcd.Spec.Replicas)))
	members := make([]*druidv1alpha1.EtcdMember, 0, numMembers)
	for i := range numMembers {
		memberName := fmt.Sprintf("%s-%d", etcd.Name, i)
		member := &druidv1alpha1.EtcdMember{
			ObjectMeta: metav1.ObjectMeta{
				Name:            memberName,
				Namespace:       etcd.Namespace,
				Labels:          getLabels(etcd, memberName),
				OwnerReferences: []metav1.OwnerReference{druidv1alpha1.GetAsOwnerReference(etcd.ObjectMeta)},
			},
		}
		if withLearnerAnnotation {
			member.Annotations = map[string]string{
				druidv1alpha1.CreateAsLearnerAnnotation: "true",
			}
		}
		members = append(members, member)
	}
	return members
}
