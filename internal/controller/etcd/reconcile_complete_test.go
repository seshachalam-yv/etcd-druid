// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcd

import (
	"context"
	"testing"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/client/kubernetes"
	"github.com/gardener/etcd-druid/internal/component"
	testutils "github.com/gardener/etcd-druid/test/utils"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	. "github.com/onsi/gomega"
)

func TestClearScaleOperationCondition(t *testing.T) {
	testCases := []struct {
		name                string
		conditions          []druidv1alpha1.Condition
		expectConditionFalse bool
		expectNoChange      bool
	}{
		{
			name:           "clears condition when it is True",
			conditions: []druidv1alpha1.Condition{
				{
					Type:               druidv1alpha1.ConditionTypeScaleOperationInProgress,
					Status:             druidv1alpha1.ConditionTrue,
					Reason:             "ScalingDown",
					Message:            "Scale-down member removal in progress",
					LastTransitionTime: metav1.Now(),
					LastUpdateTime:     metav1.Now(),
				},
			},
			expectConditionFalse: true,
		},
		{
			name:           "does nothing when condition is already False",
			conditions: []druidv1alpha1.Condition{
				{
					Type:               druidv1alpha1.ConditionTypeScaleOperationInProgress,
					Status:             druidv1alpha1.ConditionFalse,
					Reason:             "ScaleOperationCompleted",
					Message:            "Scale operation has completed successfully",
					LastTransitionTime: metav1.Now(),
					LastUpdateTime:     metav1.Now(),
				},
			},
			expectNoChange: true,
		},
		{
			name:           "does nothing when condition does not exist",
			conditions:     nil,
			expectNoChange: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			etcd := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).
				WithReplicas(1).
				Build()
			etcd.Status.Conditions = tc.conditions

			cl := testutils.NewTestClientBuilder().
				WithScheme(kubernetes.Scheme).
				WithStatusSubresource(etcd).
				WithObjects(etcd).
				Build()

			reconciler := &Reconciler{
				client: cl,
				logger: logr.Discard(),
			}

			opCtx := component.NewOperatorContext(context.Background(), logr.Discard(), uuid.NewString())
			result := reconciler.clearScaleOperationCondition(opCtx, etcd)
			g.Expect(result.NeedsRequeue()).To(BeFalse())
			g.Expect(result.GetCombinedError()).ToNot(HaveOccurred())

			if tc.expectConditionFalse {
				var found bool
				for _, c := range etcd.Status.Conditions {
					if c.Type == druidv1alpha1.ConditionTypeScaleOperationInProgress {
						found = true
						g.Expect(c.Status).To(Equal(druidv1alpha1.ConditionFalse))
						g.Expect(c.Reason).To(Equal("ScaleOperationCompleted"))
						break
					}
				}
				g.Expect(found).To(BeTrue())
			}

			if tc.expectNoChange {
				for _, c := range etcd.Status.Conditions {
					if c.Type == druidv1alpha1.ConditionTypeScaleOperationInProgress {
						g.Expect(c.Status).To(Equal(druidv1alpha1.ConditionFalse))
						break
					}
				}
			}
		})
	}
}
