package etcdopstaskprotection

import (
	"context"
	"testing"

	druidconfigv1alpha1 "github.com/gardener/etcd-druid/api/config/v1alpha1"
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/client/kubernetes"
	"github.com/gardener/etcd-druid/test/utils"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

// createTestTask is used to create a test EtcdOpsTask object which can be modified and used in the tests.
func createTestTask(name, namespace, etcdName, etcdNamespace string) *druidv1alpha1.EtcdOpsTask {
	isFinal := false
	return &druidv1alpha1.EtcdOpsTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: druidv1alpha1.EtcdOpsTaskSpec{
			EtcdRef: &druidv1alpha1.EtcdReference{
				Name:      etcdName,
				Namespace: etcdNamespace,
			},
			Config: druidv1alpha1.EtcdOpsTaskConfig{
				OnDemandSnapshot: &druidv1alpha1.OnDemandSnapshotConfig{
					Type:    druidv1alpha1.OnDemandSnapshotTypeFull,
					IsFinal: &isFinal,
				},
			},
		},
	}
}

// createEtcd is used to create a test Etcd object which can be modified and used in the tests.
func createEtcd(name, namespace string, backup bool, healthy bool) *druidv1alpha1.Etcd {
	etcd := utils.EtcdBuilderWithoutDefaults(name, namespace).WithReplicas(1).WithReadyStatus().Build()
	if backup {
		etcd.Spec.Backup.Store = &druidv1alpha1.StoreSpec{
			Container: ptr.To("test-container"),
			Prefix:    "test-prefix",
			Provider:  ptr.To(druidv1alpha1.StorageProvider("S3")),
		}
	}
	if !healthy {
		etcd.Status.Conditions = append(etcd.Status.Conditions, druidv1alpha1.Condition{
			Type:    druidv1alpha1.ConditionTypeReady,
			Status:  druidv1alpha1.ConditionFalse,
			Message: "etcd is not ready for testing purposes",
		})
	} else {
		etcd.Status.Conditions = append(etcd.Status.Conditions, druidv1alpha1.Condition{
			Type:    druidv1alpha1.ConditionTypeReady,
			Status:  druidv1alpha1.ConditionTrue,
			Message: "etcd is ready for testing purposes",
		})
	}
	return etcd
}

// TestHandleOndemandSnapshotCreation_EtcdReadiness tests the handleOnDemandSnapshot function for various Etcd readiness scenarios.
func TestHandleOndemandSnapshotCreation_EtcdReadiness(t *testing.T) {
	g := NewGomegaWithT(t)
	testCases := []struct {
		name             string
		task             *druidv1alpha1.EtcdOpsTask
		existingObjects  []client.Object // etcd will be part of this list along with duplicate tasks
		expectedResponse string
		expectErr        bool
	}{
		{
			name: "Referenced Etcd not found",
			task: createTestTask("test-task", "test-namespace", "non-existent-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createEtcd("healthy-etcd", "test-namespace", true, true),
			},
			expectedResponse: "etcd cluster referenced in spec.etcdRef does not exist",
			expectErr:        true,
		},
		{
			name: "Etcd is ready, No Duplicate CR",
			task: createTestTask("test-task", "test-namespace", "healthy-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createEtcd("healthy-etcd", "test-namespace", true, true),
			},
			expectedResponse: "OnDemandSnapshot config valid",
			expectErr:        false,
		},
		{
			name: "Etcd is ready, Duplicate CR",
			task: createTestTask("test-task", "test-namespace", "healthy-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createEtcd("healthy-etcd", "test-namespace", true, true),
				createTestTask("duplicate-task", "test-namespace", "healthy-etcd", "test-namespace"),
			},
			expectedResponse: "another EtcdOpsTask with the same etcdRef and OnDemandSnapshot config already exists",
			expectErr:        true,
		},
		{
			name: "Etcd is ready, Duplicate CR with different etcdref",
			task: createTestTask("test-task", "test-namespace", "healthy-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createEtcd("healthy-etcd", "test-namespace", true, true),
				createTestTask("duplicate-task", "test-namespace", "another-etcd", "test-namespace"),
			},
			expectedResponse: "OnDemandSnapshot config valid",
			expectErr:        false,
		},
		{
			name: "Etcd is not ready",
			task: createTestTask("test-task", "test-namespace", "unhealthy-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createEtcd("unhealthy-etcd", "test-namespace", true, false),
			},
			expectedResponse: "etcd cluster referenced in spec.etcdRef is not ready",
			expectErr:        true,
		},
		{
			name: "Backup is not enabled for Etcd",
			task: createTestTask("test-task", "test-namespace", "healthy-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createEtcd("healthy-etcd", "test-namespace", false, true),
			},
			expectedResponse: "backup is not enabled for etcd cluster referenced in spec.etcdRef",
			expectErr:        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(tc.existingObjects...).Build()

			mgr := &utils.FakeManager{
				Client: cl,
				Scheme: cl.Scheme(),
				Logger: logr.Discard(),
			}
			handler, err := NewHandler(mgr, druidconfigv1alpha1.EtcdOpsTaskWebhookConfiguration{
				Enabled: true,
			})
			g.Expect(err).ToNot(HaveOccurred())

			response := handler.handleOnDemandSnapshot(
				context.Background(),
				tc.task,
			)

			if tc.expectErr {
				g.Expect(response.Allowed).To(BeFalse())
				g.Expect(response.Result.Message).To(ContainSubstring(tc.expectedResponse))
			} else {
				g.Expect(response.Allowed).To(BeTrue())
				g.Expect(response.Result.Message).To(Equal(tc.expectedResponse))
			}
		})
	}
}
