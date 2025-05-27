package etcdoperatortask

import (
	"context"
	"testing"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/client/kubernetes"
	"github.com/gardener/etcd-druid/test/utils"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// createTestTask is used to create a test EtcdOperatorTask object which can be modified and used in the tests.
func createTestTask(name, namespace, etcdName, etcdNamespace string) *druidv1alpha1.EtcdOperatorTask {
	isFinal := false
	return &druidv1alpha1.EtcdOperatorTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: druidv1alpha1.EtcdOperatorTaskSpec{
			EtcdRef: &druidv1alpha1.EtcdReference{
				Name:      etcdName,
				Namespace: etcdNamespace,
			},
			Config: druidv1alpha1.EtcdOperatorTaskConfig{
				OnDemandSnapshot: &druidv1alpha1.OnDemandSnapshotConfig{
					Type:    druidv1alpha1.OnDemandSnapshotTypeFull,
					IsFinal: &isFinal,
				},
			},
		},
	}
}

func createhealthyEtcd(name, namespace string, backup bool) *druidv1alpha1.Etcd {
	etcd := utils.EtcdBuilderWithoutDefaults(name, namespace).WithReplicas(1).WithReadyStatus().Build()
	if backup {
		etcd.Spec.Backup.Store = &druidv1alpha1.StoreSpec{
			Container: ptr.To("test-container"),
			Prefix:    "test-prefix",
			Provider:  ptr.To(druidv1alpha1.StorageProvider("S3")),
		}
	}
	etcd.Status.Conditions = append(etcd.Status.Conditions, druidv1alpha1.Condition{
		Type:    druidv1alpha1.ConditionTypeReady,
		Status:  druidv1alpha1.ConditionTrue,
		Message: "etcd is ready for testing purposes",
	})
	return etcd
}

func createUnhealthyEtcd(name, namespace string) *druidv1alpha1.Etcd {
	etcd := utils.EtcdBuilderWithoutDefaults(name, namespace).WithReplicas(1).Build()
	// Set status to unhealthy (not ready)
	etcd.Status.Conditions = append(etcd.Status.Conditions, druidv1alpha1.Condition{
		Type:    druidv1alpha1.ConditionTypeReady,
		Status:  druidv1alpha1.ConditionFalse,
		Message: "etcd is not ready for testing purposes",
	})
	return etcd
}

func TestHandleOndemandSnapshotCreation_EtcdReadiness(t *testing.T) {
	g := NewGomegaWithT(t)
	testCases := []struct {
		name             string
		task             *druidv1alpha1.EtcdOperatorTask
		existingObjects  []client.Object // etcd will be part of this list along with duplicate tasks
		expectedResponse string
		expectErr 	     bool
	}{
		{
			name: "Referenced Etcd not found",
			task: createTestTask("test-task", "test-namespace", "non-existent-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createhealthyEtcd("healthy-etcd", "test-namespace", true),
			},
			expectedResponse: "etcd cluster referenced in spec.etcdRef does not exist",
			expectErr:        true,
		},
		{
			name: "Etcd is ready, No Duplicate CR",
			task: createTestTask("test-task", "test-namespace", "healthy-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createhealthyEtcd("healthy-etcd", "test-namespace", true),
			},
			expectedResponse: "OnDemandSnapshot config valid",
			expectErr:        false,
		},
		{
			name: "Etcd is ready, Duplicate CR",
			task: createTestTask("test-task", "test-namespace", "healthy-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createhealthyEtcd("healthy-etcd", "test-namespace", true),
				createTestTask("duplicate-task", "test-namespace", "healthy-etcd", "test-namespace"),
			},
			expectedResponse: "another EtcdOperatorTask with the same etcdRef and OnDemandSnapshot config already exists",
			expectErr:        true,
		},
		{
			name: "Etcd is ready, Duplicate CR with different etcdref",
			task: createTestTask("test-task", "test-namespace", "healthy-etcd", "test-namespace"),
			existingObjects: []client.Object{
				createhealthyEtcd("healthy-etcd", "test-namespace", true),
				createTestTask("duplicate-task", "test-namespace", "another-etcd", "test-namespace"),
			},
			expectedResponse: "OnDemandSnapshot config valid",
			expectErr:        false,
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
			config := Config{
				Enabled: true,
			}
			handler, err := NewHandler(mgr, &config)
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
