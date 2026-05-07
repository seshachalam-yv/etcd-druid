// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package removemembers

import (
	"context"
	"testing"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/client/kubernetes"
	taskhandler "github.com/gardener/etcd-druid/internal/controller/etcdopstask/handler"
	druiderr "github.com/gardener/etcd-druid/internal/errors"
	"github.com/gardener/etcd-druid/test/utils"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/gomega"
)

const (
	testEtcdName      = "test-etcd"
	testNamespace     = "test-namespace"
	testTaskName      = "test-task"
	testBRImage       = "europe-docker.pkg.dev/gardener-project/releases/gardener/etcdbrctl:v0.43.0"
	testMemberName    = "test-etcd-2"
	testMemberPeerURL = "https://test-etcd-2.test-etcd-peer.test-namespace.svc:2380"
)

func TestRemoveMembersHandler_Admit_EtcdReady(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, true)
	objs := []client.Object{etcd}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Admit(context.Background())
	g.Expect(result.Requeue).To(BeFalse())
	g.Expect(result.Description).To(Equal("Admit check passed"))
	g.Expect(result.Error).To(BeNil())
}

func TestRemoveMembersHandler_Admit_EtcdNotReady(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, false)
	objs := []client.Object{etcd}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Admit(context.Background())
	g.Expect(result.Requeue).To(BeFalse())
	g.Expect(result.Description).To(Equal("Etcd is not ready"))
	g.Expect(result.Error).ToNot(BeNil())

	druidErr := result.Error.(*druiderr.DruidError)
	g.Expect(druidErr.Code).To(Equal(ErrEtcdNotReady))
	g.Expect(druidErr.Operation).To(Equal(string(druidv1alpha1.LastOperationTypeAdmit)))
}

func TestRemoveMembersHandler_Admit_EtcdNotFound(t *testing.T) {
	g := NewGomegaWithT(t)

	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).Build()
	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Admit(context.Background())
	g.Expect(result.Requeue).To(BeFalse())
	g.Expect(result.Description).To(Equal("Etcd object not found"))
	g.Expect(result.Error).ToNot(BeNil())

	druidErr := result.Error.(*druiderr.DruidError)
	g.Expect(druidErr.Code).To(Equal(taskhandler.ErrGetEtcd))
}

func TestRemoveMembersHandler_Execute_CreatesJob(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, true)
	sts := createStatefulSet(testEtcdName, testNamespace)
	objs := []client.Object{etcd, sts}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Execute(context.Background())
	g.Expect(result.Description).To(Equal("Member removal Job created successfully"))
	g.Expect(result.Requeue).To(BeTrue())
	g.Expect(result.Error).To(BeNil())

	// Verify the Job was created.
	job := &batchv1.Job{}
	jobName := druidv1alpha1.GetMemberRemoveJobName(etcd.ObjectMeta)
	err = cl.Get(context.Background(), client.ObjectKey{Name: jobName, Namespace: testNamespace}, job)
	g.Expect(err).To(BeNil())
	g.Expect(job.Name).To(Equal(jobName))
	g.Expect(job.Spec.Template.Spec.Containers).To(HaveLen(1))
	g.Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal(testBRImage))
	g.Expect(*job.Spec.BackoffLimit).To(Equal(int32(3)))
	g.Expect(*job.Spec.ActiveDeadlineSeconds).To(Equal(int64(300)))
	// Check owner reference points to EtcdOpsTask.
	g.Expect(job.OwnerReferences).To(HaveLen(1))
	g.Expect(job.OwnerReferences[0].Kind).To(Equal("EtcdOpsTask"))
	g.Expect(job.OwnerReferences[0].Name).To(Equal(testTaskName))
}

func TestRemoveMembersHandler_Execute_JobSucceeded(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, true)
	sts := createStatefulSet(testEtcdName, testNamespace)
	job := createMemberRemoveJob(testEtcdName, testNamespace, batchv1.JobComplete)
	objs := []client.Object{etcd, sts, job}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Execute(context.Background())
	g.Expect(result.Requeue).To(BeFalse())
	g.Expect(result.Description).To(Equal("Member removal Job completed successfully"))
	g.Expect(result.Error).To(BeNil())
}

func TestRemoveMembersHandler_Execute_JobFailed(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, true)
	sts := createStatefulSet(testEtcdName, testNamespace)
	job := createMemberRemoveJob(testEtcdName, testNamespace, batchv1.JobFailed)
	objs := []client.Object{etcd, sts, job}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Execute(context.Background())
	g.Expect(result.Requeue).To(BeFalse())
	g.Expect(result.Description).To(Equal("Member removal Job has failed"))
	g.Expect(result.Error).ToNot(BeNil())

	druidErr := result.Error.(*druiderr.DruidError)
	g.Expect(druidErr.Code).To(Equal(ErrJobFailed))
}

func TestRemoveMembersHandler_Execute_JobActive(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, true)
	sts := createStatefulSet(testEtcdName, testNamespace)
	job := createActiveJob(testEtcdName, testNamespace)
	objs := []client.Object{etcd, sts, job}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Execute(context.Background())
	g.Expect(result.Requeue).To(BeTrue())
	g.Expect(result.Description).To(Equal("Member removal Job is still running"))
	g.Expect(result.Error).To(BeNil())
}

func TestRemoveMembersHandler_Execute_EtcdNotReady(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, false)
	objs := []client.Object{etcd}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Execute(context.Background())
	g.Expect(result.Requeue).To(BeFalse())
	g.Expect(result.Description).To(Equal("Etcd is not ready"))
	g.Expect(result.Error).ToNot(BeNil())
}

func TestRemoveMembersHandler_Execute_WithTLS(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcdWithTLS(testEtcdName, testNamespace)
	sts := createStatefulSet(testEtcdName, testNamespace)
	objs := []client.Object{etcd, sts}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Execute(context.Background())
	g.Expect(result.Description).To(Equal("Member removal Job created successfully"))
	g.Expect(result.Requeue).To(BeTrue())
	g.Expect(result.Error).To(BeNil())

	// Verify TLS volumes are configured.
	job := &batchv1.Job{}
	jobName := druidv1alpha1.GetMemberRemoveJobName(etcd.ObjectMeta)
	err = cl.Get(context.Background(), client.ObjectKey{Name: jobName, Namespace: testNamespace}, job)
	g.Expect(err).To(BeNil())
	g.Expect(job.Spec.Template.Spec.Volumes).To(HaveLen(2))
	g.Expect(job.Spec.Template.Spec.Containers[0].VolumeMounts).To(HaveLen(2))
}

func TestRemoveMembersHandler_Cleanup(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, true)
	job := createActiveJob(testEtcdName, testNamespace)
	objs := []client.Object{etcd, job}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Cleanup(context.Background())
	g.Expect(result.Requeue).To(BeFalse())
	g.Expect(result.Description).To(Equal("Cleanup completed"))
	g.Expect(result.Error).To(BeNil())
}

func TestRemoveMembersHandler_Cleanup_NoJob(t *testing.T) {
	g := NewGomegaWithT(t)

	etcd := createEtcd(testEtcdName, testNamespace, true)
	objs := []client.Object{etcd}
	cl := utils.NewTestClientBuilder().WithScheme(kubernetes.Scheme).WithObjects(objs...).Build()

	etcdOpsTask := createEtcdOpsTask()
	taskHandler, err := New(cl, etcdOpsTask, nil)
	g.Expect(err).To(BeNil())

	result := taskHandler.Cleanup(context.Background())
	g.Expect(result.Requeue).To(BeFalse())
	g.Expect(result.Description).To(Equal("Cleanup completed"))
	g.Expect(result.Error).To(BeNil())
}

// --- Helper functions ---

func createEtcdOpsTask() *druidv1alpha1.EtcdOpsTask {
	return utils.EtcdOpsTaskBuilderWithDefaults(testTaskName, testNamespace).
		WithEtcdName(testEtcdName).
		WithRemoveMembersConfig(&druidv1alpha1.RemoveMembersConfig{
			MembersToRemove: []druidv1alpha1.MemberToRemove{
				{
					Name:    testMemberName,
					PeerURL: testMemberPeerURL,
				},
			},
		}).Build()
}

func createEtcd(name, namespace string, ready bool) *druidv1alpha1.Etcd {
	etcdBuilder := utils.EtcdBuilderWithoutDefaults(name, namespace).WithReplicas(3).WithReadyStatus()
	etcd := etcdBuilder.Build()

	if !ready {
		etcd.Status.Conditions = []druidv1alpha1.Condition{
			{
				Type:    druidv1alpha1.ConditionTypeReady,
				Status:  druidv1alpha1.ConditionFalse,
				Message: "etcd is not ready",
			},
		}
	} else {
		etcd.Status.Conditions = append(etcd.Status.Conditions, druidv1alpha1.Condition{
			Type:    druidv1alpha1.ConditionTypeReady,
			Status:  druidv1alpha1.ConditionTrue,
			Message: "etcd is ready",
		})
	}

	return etcd
}

func createEtcdWithTLS(name, namespace string) *druidv1alpha1.Etcd {
	etcdBuilder := utils.EtcdBuilderWithoutDefaults(name, namespace).WithReplicas(3).WithReadyStatus().WithClientTLS()
	etcd := etcdBuilder.Build()
	etcd.Status.Conditions = append(etcd.Status.Conditions, druidv1alpha1.Condition{
		Type:    druidv1alpha1.ConditionTypeReady,
		Status:  druidv1alpha1.ConditionTrue,
		Message: "etcd is ready",
	})
	return etcd
}

func createStatefulSet(etcdName, namespace string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      druidv1alpha1.GetStatefulSetName(metav1.ObjectMeta{Name: etcdName}),
			Namespace: namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": etcdName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": etcdName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "etcd",
							Image: "europe-docker.pkg.dev/gardener-project/releases/gardener/etcd-wrapper:v0.3.0",
						},
						{
							Name:  "backup-restore",
							Image: testBRImage,
						},
					},
				},
			},
		},
	}
}

func createMemberRemoveJob(etcdName, namespace string, conditionType batchv1.JobConditionType) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      druidv1alpha1.GetMemberRemoveJobName(metav1.ObjectMeta{Name: etcdName}),
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  containerNameMemberRemove,
							Image: testBRImage,
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{
					Type:   conditionType,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	if conditionType == batchv1.JobComplete {
		job.Status.Succeeded = 1
	} else if conditionType == batchv1.JobFailed {
		job.Status.Failed = 1
	}

	return job
}

func createActiveJob(etcdName, namespace string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      druidv1alpha1.GetMemberRemoveJobName(metav1.ObjectMeta{Name: etcdName}),
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(jobBackoffLimit),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  containerNameMemberRemove,
							Image: testBRImage,
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
		Status: batchv1.JobStatus{
			Active: 1,
		},
	}
}

