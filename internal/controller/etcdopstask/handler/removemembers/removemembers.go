// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package removemembers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	druidapicommon "github.com/gardener/etcd-druid/api/common"
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/common"
	taskhandler "github.com/gardener/etcd-druid/internal/controller/etcdopstask/handler"
	"github.com/gardener/etcd-druid/internal/controller/etcdopstask/handler/utils"
	druiderr "github.com/gardener/etcd-druid/internal/errors"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ErrEtcdNotReady represents the error when etcd is not ready.
	ErrEtcdNotReady druidapicommon.ErrorCode = "ERR_ETCD_NOT_READY"
	// ErrCreateJob represents the error when the member-remove Job cannot be created.
	ErrCreateJob druidapicommon.ErrorCode = "ERR_CREATE_JOB"
	// ErrGetJob represents the error when the member-remove Job cannot be fetched.
	ErrGetJob druidapicommon.ErrorCode = "ERR_GET_JOB"
	// ErrJobFailed represents the error when the member-remove Job has failed.
	ErrJobFailed druidapicommon.ErrorCode = "ERR_JOB_FAILED"
	// ErrDeleteJob represents the error when the member-remove Job cannot be deleted.
	ErrDeleteJob druidapicommon.ErrorCode = "ERR_DELETE_JOB"

	// jobPollInterval is the interval between polling attempts for job status.
	jobPollInterval = 2 * time.Second
	// jobPollTimeout is the maximum time to wait for a job to complete.
	jobPollTimeout = 5 * time.Minute

	// containerName is the name of the container in the member-remove Job.
	containerName = "member-remove"
)

// handler implements the taskhandler.Handler interface for removing etcd members.
type handler struct {
	k8sClient     client.Client
	task          *druidv1alpha1.EtcdOpsTask
	etcdReference types.NamespacedName
	config        druidv1alpha1.RemoveMembersConfig
}

// New creates a new instance of the RemoveMembers handler.
func New(k8sClient client.Client, task *druidv1alpha1.EtcdOpsTask, _ *http.Client) (taskhandler.Handler, error) {
	etcdRef := task.GetEtcdReference()
	return &handler{
		k8sClient:     k8sClient,
		task:          task,
		etcdReference: etcdRef,
		config:        *task.Spec.Config.RemoveMembers,
	}, nil
}

// Admit checks if the task is permitted to run.
// It fetches the Etcd CR and verifies that the EtcdReady condition is True.
func (h *handler) Admit(ctx context.Context) taskhandler.Result {
	etcd, errResult := utils.GetEtcd(ctx, h.k8sClient, h.etcdReference, druidv1alpha1.LastOperationTypeAdmit)
	if errResult != nil {
		return *errResult
	}

	if !etcd.IsReady() {
		return taskhandler.Result{
			Description: "Etcd is not ready",
			Error:       druiderr.WrapError(fmt.Errorf("etcd is not ready"), ErrEtcdNotReady, string(druidv1alpha1.LastOperationTypeAdmit), "etcd is not ready, cannot proceed with member removal"),
			Requeue:     false,
		}
	}

	return taskhandler.Result{
		Description: "Admit check passed",
		Requeue:     false,
	}
}

// Execute creates a Kubernetes Job running `etcdbrctl member-remove` and polls for its completion.
func (h *handler) Execute(ctx context.Context) taskhandler.Result {
	etcd, errResult := utils.GetEtcd(ctx, h.k8sClient, h.etcdReference, druidv1alpha1.LastOperationTypeExecution)
	if errResult != nil {
		return *errResult
	}

	// Re-check readiness upon requeues to account for etcd readiness changes since Admit() is run only once.
	if !etcd.IsReady() {
		return taskhandler.Result{
			Description: "Etcd is not ready",
			Error:       druiderr.WrapError(fmt.Errorf("etcd is not ready"), ErrEtcdNotReady, string(druidv1alpha1.LastOperationTypeExecution), "etcd is not ready, cannot proceed with member removal"),
			Requeue:     false,
		}
	}

	jobName := getJobName(etcd)
	jobNamespace := h.task.Namespace

	// Check if the Job already exists (idempotency on requeue).
	existingJob := &batchv1.Job{}
	err := h.k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: jobNamespace}, existingJob)
	if err != nil && !apierrors.IsNotFound(err) {
		return taskhandler.Result{
			Description: "Failed to get member-remove Job",
			Error:       druiderr.WrapError(err, ErrGetJob, string(druidv1alpha1.LastOperationTypeExecution), "failed to get member-remove job"),
			Requeue:     true,
		}
	}

	if apierrors.IsNotFound(err) {
		// Create the Job.
		job := h.buildJob(etcd)
		if createErr := h.k8sClient.Create(ctx, job); createErr != nil {
			return taskhandler.Result{
				Description: "Failed to create member-remove Job",
				Error:       druiderr.WrapError(createErr, ErrCreateJob, string(druidv1alpha1.LastOperationTypeExecution), "failed to create member-remove job"),
				Requeue:     true,
			}
		}
		existingJob = job
	}

	// Poll for Job completion.
	var completedJob *batchv1.Job
	pollErr := wait.PollUntilContextTimeout(ctx, jobPollInterval, jobPollTimeout, true, func(ctx context.Context) (bool, error) {
		j := &batchv1.Job{}
		if getErr := h.k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: jobNamespace}, j); getErr != nil {
			return false, nil // retry on transient errors
		}
		if isJobFinished(j) {
			completedJob = j
			return true, nil
		}
		return false, nil
	})

	if pollErr != nil {
		return taskhandler.Result{
			Description: "Timed out waiting for member-remove Job to complete",
			Error:       druiderr.WrapError(pollErr, ErrJobFailed, string(druidv1alpha1.LastOperationTypeExecution), "timed out waiting for member-remove job to complete"),
			Requeue:     true,
		}
	}

	if isJobSucceeded(completedJob) {
		return taskhandler.Result{
			Description: "Member removal completed successfully",
			Requeue:     false,
		}
	}

	return taskhandler.Result{
		Description: "Member-remove Job failed",
		Error:       druiderr.WrapError(fmt.Errorf("member-remove job %s/%s failed", jobNamespace, jobName), ErrJobFailed, string(druidv1alpha1.LastOperationTypeExecution), "member-remove job failed"),
		Requeue:     false,
	}
}

// Cleanup deletes the member-remove Job with foreground propagation.
func (h *handler) Cleanup(ctx context.Context) taskhandler.Result {
	etcd, errResult := utils.GetEtcd(ctx, h.k8sClient, h.etcdReference, druidv1alpha1.LastOperationTypeCleanup)
	if errResult != nil {
		return *errResult
	}

	jobName := getJobName(etcd)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: h.task.Namespace,
		},
	}

	if err := h.k8sClient.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		if apierrors.IsNotFound(err) {
			return taskhandler.Result{
				Description: "Cleanup completed, Job already deleted",
				Requeue:     false,
			}
		}
		return taskhandler.Result{
			Description: "Failed to delete member-remove Job",
			Error:       druiderr.WrapError(err, ErrDeleteJob, string(druidv1alpha1.LastOperationTypeCleanup), "failed to delete member-remove job"),
			Requeue:     true,
		}
	}

	return taskhandler.Result{
		Description: "Cleanup completed",
		Requeue:     false,
	}
}

// buildJob constructs the Kubernetes Job spec for the member-remove operation.
func (h *handler) buildJob(etcd *druidv1alpha1.Etcd) *batchv1.Job {
	jobName := getJobName(etcd)
	args := h.buildJobArgs(etcd)
	volumes, volumeMounts := getJobTLSVolumesAndMounts(etcd)

	// Use the backup-restore image from the Etcd spec, or fall back to the StatefulSet
	image := ptr.Deref(etcd.Spec.Backup.Image, "")
	if image == "" {
		image = h.getBackupRestoreImageFromSTS(etcd)
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: h.task.Namespace,
			Labels:    getJobLabels(etcd, jobName),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         druidv1alpha1.SchemeGroupVersion.String(),
					Kind:               "EtcdOpsTask",
					Name:               h.task.Name,
					UID:                h.task.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To[int32](3),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: getJobLabels(etcd, jobName),
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: druidv1alpha1.GetServiceAccountName(etcd.ObjectMeta),
					Containers: []corev1.Container{
						{
							Name:            containerName,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            args,
							VolumeMounts:    volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	return job
}

// buildJobArgs constructs the command-line arguments for the etcdbrctl member-remove command.
func (h *handler) buildJobArgs(etcd *druidv1alpha1.Etcd) []string {
	args := []string{"member-remove"}

	// Add --member=<name>=<peerURL> for each member to remove.
	for _, member := range h.config.MembersToRemove {
		args = append(args, fmt.Sprintf("--member=%s=%s", member.Name, member.PeerURL))
	}

	// Add etcd client endpoint.
	clientPort := ptr.Deref(etcd.Spec.Etcd.ClientPort, common.DefaultPortEtcdClient)
	clientURL := fmt.Sprintf("https://%s:%d", druidv1alpha1.GetClientHostname(etcd), clientPort)

	// Determine TLS configuration.
	if etcd.Spec.Etcd.ClientUrlTLS == nil {
		// Non-TLS mode.
		clientURL = fmt.Sprintf("http://%s:%d", druidv1alpha1.GetClientHostname(etcd), clientPort)
		args = append(args, "--insecure-transport=true")
	} else {
		args = append(args, "--insecure-transport=false")
		args = append(args, fmt.Sprintf("--cacert=%s/bundle.crt", common.VolumeMountPathEtcdCA))
		args = append(args, fmt.Sprintf("--cert=%s/tls.crt", common.VolumeMountPathEtcdClientTLS))
		args = append(args, fmt.Sprintf("--key=%s/tls.key", common.VolumeMountPathEtcdClientTLS))
	}

	args = append(args, fmt.Sprintf("--endpoints=%s", clientURL))

	return args
}

// getJobTLSVolumesAndMounts returns the volumes and volume mounts for TLS configuration.
func getJobTLSVolumesAndMounts(etcd *druidv1alpha1.Etcd) ([]corev1.Volume, []corev1.VolumeMount) {
	if etcd.Spec.Etcd.ClientUrlTLS == nil {
		return nil, nil
	}

	tlsConfig := etcd.Spec.Etcd.ClientUrlTLS
	volumes := []corev1.Volume{
		{
			Name: common.VolumeNameEtcdCA,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: tlsConfig.TLSCASecretRef.Name,
				},
			},
		},
		{
			Name: common.VolumeNameEtcdClientTLS,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: tlsConfig.ClientTLSSecretRef.Name,
				},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{
			Name:      common.VolumeNameEtcdCA,
			MountPath: common.VolumeMountPathEtcdCA,
			ReadOnly:  true,
		},
		{
			Name:      common.VolumeNameEtcdClientTLS,
			MountPath: common.VolumeMountPathEtcdClientTLS,
			ReadOnly:  true,
		},
	}

	return volumes, volumeMounts
}

// getJobName returns the job name for the member-remove operation, truncated to 63 chars.
func getJobName(etcd *druidv1alpha1.Etcd) string {
	name := druidv1alpha1.GetMemberRemoveJobName(etcd.ObjectMeta)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// getJobLabels returns the labels for the member-remove Job.
func getJobLabels(etcd *druidv1alpha1.Etcd, jobName string) map[string]string {
	labels := druidv1alpha1.GetDefaultLabels(etcd.ObjectMeta)
	labels[druidv1alpha1.LabelAppNameKey] = jobName
	labels[druidv1alpha1.LabelComponentKey] = "etcd-member-remove"
	return labels
}

// isJobFinished returns true if the Job has completed (succeeded or failed).
func isJobFinished(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// isJobSucceeded returns true if the Job completed successfully.
func isJobSucceeded(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// getBackupRestoreImageFromSTS retrieves the backup-restore container image from the Etcd's StatefulSet.
func (h *handler) getBackupRestoreImageFromSTS(etcd *druidv1alpha1.Etcd) string {
	sts := &appsv1.StatefulSet{}
	stsName := druidv1alpha1.GetStatefulSetName(etcd.ObjectMeta)
	if err := h.k8sClient.Get(context.Background(), types.NamespacedName{Name: stsName, Namespace: etcd.Namespace}, sts); err != nil {
		return ""
	}
	for _, container := range sts.Spec.Template.Spec.Containers {
		if container.Name == "backup-restore" {
			return container.Image
		}
	}
	return ""
}
