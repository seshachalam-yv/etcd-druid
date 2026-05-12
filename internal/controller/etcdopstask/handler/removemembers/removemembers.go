// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package removemembers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	druidapicommon "github.com/gardener/etcd-druid/api/common"
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/common"
	taskhandler "github.com/gardener/etcd-druid/internal/controller/etcdopstask/handler"
	"github.com/gardener/etcd-druid/internal/controller/etcdopstask/handler/utils"
	druiderr "github.com/gardener/etcd-druid/internal/errors"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ErrEtcdNotReady represents the error when etcd is not ready.
	ErrEtcdNotReady druidapicommon.ErrorCode = "ERR_ETCD_NOT_READY"
	// ErrGetStatefulSet represents the error when fetching the StatefulSet fails.
	ErrGetStatefulSet druidapicommon.ErrorCode = "ERR_GET_STATEFULSET"
	// ErrBackupRestoreImageNotFound represents the error when the backup-restore image cannot be determined.
	ErrBackupRestoreImageNotFound druidapicommon.ErrorCode = "ERR_BACKUP_RESTORE_IMAGE_NOT_FOUND"
	// ErrCreateJob represents the error when Job creation fails.
	ErrCreateJob druidapicommon.ErrorCode = "ERR_CREATE_JOB"
	// ErrGetJob represents the error when fetching the Job fails.
	ErrGetJob druidapicommon.ErrorCode = "ERR_GET_JOB"
	// ErrJobFailed represents the error when the Job has failed.
	ErrJobFailed druidapicommon.ErrorCode = "ERR_JOB_FAILED"
	// ErrDeleteJob represents the error when Job deletion fails.
	ErrDeleteJob druidapicommon.ErrorCode = "ERR_DELETE_JOB"

	containerNameMemberRemove = "member-remove"
	jobBackoffLimit           = int32(3)
	jobActiveDeadlineSeconds  = int64(300)
)

// handler implements the taskhandler.Handler interface for member removal tasks.
type handler struct {
	k8sClient     client.Client
	task          *druidv1alpha1.EtcdOpsTask
	etcdReference types.NamespacedName
	config        druidv1alpha1.RemoveMembersConfig
}

// New creates a new RemoveMembers task handler.
func New(k8sClient client.Client, task *druidv1alpha1.EtcdOpsTask, _ *http.Client) (taskhandler.Handler, error) {
	etcdRef := task.GetEtcdReference()
	return &handler{
		k8sClient:     k8sClient,
		task:          task,
		etcdReference: etcdRef,
		config:        *task.Spec.Config.RemoveMembers,
	}, nil
}

// Admit checks if the task can be admitted for execution.
func (h *handler) Admit(ctx context.Context) taskhandler.Result {
	etcd, errResult := utils.GetEtcd(ctx, h.k8sClient, h.etcdReference, druidv1alpha1.LastOperationTypeAdmit)
	if errResult != nil {
		return *errResult
	}

	if !etcd.IsReady() {
		return taskhandler.Result{
			Description: "Etcd is not ready",
			Error:       druiderr.WrapError(fmt.Errorf("etcd is not ready"), ErrEtcdNotReady, string(druidv1alpha1.LastOperationTypeAdmit), "etcd is not ready"),
			Requeue:     false,
		}
	}

	return taskhandler.Result{
		Description: "Admit check passed",
		Requeue:     false,
	}
}

// Execute performs the member removal task by creating or monitoring a Job.
func (h *handler) Execute(ctx context.Context) taskhandler.Result {
	etcd, errResult := utils.GetEtcd(ctx, h.k8sClient, h.etcdReference, druidv1alpha1.LastOperationTypeExecution)
	if errResult != nil {
		return *errResult
	}

	// Re-check readiness upon requeues.
	if !etcd.IsReady() {
		return taskhandler.Result{
			Description: "Etcd is not ready",
			Error:       druiderr.WrapError(fmt.Errorf("etcd is not ready"), ErrEtcdNotReady, string(druidv1alpha1.LastOperationTypeExecution), "etcd is not ready"),
			Requeue:     false,
		}
	}

	jobName := druidv1alpha1.GetMemberRemoveJobName(etcd.ObjectMeta)
	existingJob := &batchv1.Job{}
	err := h.k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: h.task.Namespace}, existingJob)
	if err == nil {
		// Job exists, check its status.
		return h.checkJobStatus(existingJob)
	}
	if !apierrors.IsNotFound(err) {
		return taskhandler.Result{
			Description: "Failed to get member-remove Job",
			Error:       druiderr.WrapError(err, ErrGetJob, string(druidv1alpha1.LastOperationTypeExecution), "failed to get member-remove Job"),
			Requeue:     true,
		}
	}

	// Job does not exist, create it.
	return h.createJob(ctx, etcd)
}

// Cleanup deletes the member-remove Job if it exists.
func (h *handler) Cleanup(ctx context.Context) taskhandler.Result {
	etcd, errResult := utils.GetEtcd(ctx, h.k8sClient, h.etcdReference, druidv1alpha1.LastOperationTypeCleanup)
	if errResult != nil {
		// If etcd is not found, there's nothing to clean up.
		return taskhandler.Result{
			Description: "Cleanup completed",
			Requeue:     false,
		}
	}

	jobName := druidv1alpha1.GetMemberRemoveJobName(etcd.ObjectMeta)
	job := &batchv1.Job{}
	err := h.k8sClient.Get(ctx, types.NamespacedName{Name: jobName, Namespace: h.task.Namespace}, job)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return taskhandler.Result{
				Description: "Cleanup completed",
				Requeue:     false,
			}
		}
		return taskhandler.Result{
			Description: "Failed to get member-remove Job during cleanup",
			Error:       druiderr.WrapError(err, ErrDeleteJob, string(druidv1alpha1.LastOperationTypeCleanup), "failed to get member-remove Job during cleanup"),
			Requeue:     true,
		}
	}

	propagation := metav1.DeletePropagationForeground
	if err := h.k8sClient.Delete(ctx, job, &client.DeleteOptions{
		PropagationPolicy: &propagation,
	}); err != nil && !apierrors.IsNotFound(err) {
		return taskhandler.Result{
			Description: "Failed to delete member-remove Job",
			Error:       druiderr.WrapError(err, ErrDeleteJob, string(druidv1alpha1.LastOperationTypeCleanup), "failed to delete member-remove Job"),
			Requeue:     true,
		}
	}

	return taskhandler.Result{
		Description: "Cleanup completed",
		Requeue:     false,
	}
}

// checkJobStatus evaluates the current status of the member-remove Job.
func (h *handler) checkJobStatus(job *batchv1.Job) taskhandler.Result {
	if job.Status.Succeeded > 0 {
		return taskhandler.Result{
			Description: "Member removal Job completed successfully",
			Requeue:     false,
		}
	}

	if job.Status.Failed > 0 && isJobFinished(job) {
		return taskhandler.Result{
			Description: "Member removal Job has failed",
			Error:       druiderr.WrapError(fmt.Errorf("member removal Job %s has failed", job.Name), ErrJobFailed, string(druidv1alpha1.LastOperationTypeExecution), "member removal Job has failed"),
			Requeue:     false,
		}
	}

	// Job is still active.
	return taskhandler.Result{
		Description: "Member removal Job is still running",
		Requeue:     true,
	}
}

// isJobFinished checks if a Job has reached a terminal condition (Complete or Failed).
func isJobFinished(job *batchv1.Job) bool {
	for _, c := range job.Status.Conditions {
		if (c.Type == batchv1.JobComplete || c.Type == batchv1.JobFailed) && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// createJob creates the member-remove Job.
func (h *handler) createJob(ctx context.Context, etcd *druidv1alpha1.Etcd) taskhandler.Result {
	// Get backup-restore image from the StatefulSet.
	image, errResult := h.getBackupRestoreImage(ctx, etcd)
	if errResult != nil {
		return *errResult
	}

	job := h.buildJob(etcd, image)
	if err := h.k8sClient.Create(ctx, job); err != nil {
		return taskhandler.Result{
			Description: "Failed to create member-remove Job",
			Error:       druiderr.WrapError(err, ErrCreateJob, string(druidv1alpha1.LastOperationTypeExecution), "failed to create member-remove Job"),
			Requeue:     true,
		}
	}

	return taskhandler.Result{
		Description: "Member removal Job created successfully",
		Requeue:     true,
	}
}

// getBackupRestoreImage gets the backup-restore container image from the existing StatefulSet.
func (h *handler) getBackupRestoreImage(ctx context.Context, etcd *druidv1alpha1.Etcd) (string, *taskhandler.Result) {
	stsName := druidv1alpha1.GetStatefulSetName(etcd.ObjectMeta)
	sts := &appsv1.StatefulSet{}
	if err := h.k8sClient.Get(ctx, types.NamespacedName{Name: stsName, Namespace: etcd.Namespace}, sts); err != nil {
		return "", &taskhandler.Result{
			Description: "Failed to get StatefulSet",
			Error:       druiderr.WrapError(err, ErrGetStatefulSet, string(druidv1alpha1.LastOperationTypeExecution), "failed to get StatefulSet to determine backup-restore image"),
			Requeue:     true,
		}
	}

	for _, container := range sts.Spec.Template.Spec.Containers {
		if container.Name == "backup-restore" {
			return container.Image, nil
		}
	}

	return "", &taskhandler.Result{
		Description: "Backup-restore container image not found in StatefulSet",
		Error:       druiderr.WrapError(fmt.Errorf("backup-restore container not found in StatefulSet %s", stsName), ErrBackupRestoreImageNotFound, string(druidv1alpha1.LastOperationTypeExecution), "backup-restore container image not found in StatefulSet"),
		Requeue:     false,
	}
}

// buildJob constructs the batchv1.Job spec for member removal.
func (h *handler) buildJob(etcd *druidv1alpha1.Etcd, image string) *batchv1.Job {
	args := h.buildJobArgs(etcd)
	volumes, volumeMounts := h.buildTLSVolumesAndMounts(etcd)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      druidv1alpha1.GetMemberRemoveJobName(etcd.ObjectMeta),
			Namespace: h.task.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "etcd",
				"app.kubernetes.io/instance":   etcd.Name,
				"app.kubernetes.io/managed-by": "etcd-druid",
				"app.kubernetes.io/component":  "member-remove",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         druidv1alpha1.SchemeGroupVersion.String(),
					BlockOwnerDeletion: ptr.To(true),
					Controller:         ptr.To(true),
					Kind:               "EtcdOpsTask",
					Name:               h.task.Name,
					UID:                h.task.UID,
				},
			},
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: ptr.To(jobActiveDeadlineSeconds),
			Completions:           ptr.To[int32](1),
			BackoffLimit:          ptr.To(jobBackoffLimit),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app.kubernetes.io/name":       "etcd",
						"app.kubernetes.io/instance":   etcd.Name,
						"app.kubernetes.io/managed-by": "etcd-druid",
						"app.kubernetes.io/component":  "member-remove",
					},
				},
				Spec: corev1.PodSpec{
					ActiveDeadlineSeconds:         ptr.To(jobActiveDeadlineSeconds),
					TerminationGracePeriodSeconds: ptr.To[int64](30),
					ServiceAccountName:            druidv1alpha1.GetServiceAccountName(etcd.ObjectMeta),
					RestartPolicy:                 corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:            containerNameMemberRemove,
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            args,
							VolumeMounts:    volumeMounts,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	return job
}

// buildJobArgs constructs the command-line arguments for the member-remove container.
func (h *handler) buildJobArgs(etcd *druidv1alpha1.Etcd) []string {
	clientPort := ptr.Deref(etcd.Spec.Etcd.ClientPort, common.DefaultPortEtcdClient)
	// Connect directly to the surviving member (lowest ordinal) via pod DNS to ensure
	// MemberRemove operations are committed by the member that will remain after scale-down.
	survivingPodName := druidv1alpha1.GetOrdinalPodName(etcd.ObjectMeta, 0)
	peerSvcName := druidv1alpha1.GetPeerServiceName(etcd.ObjectMeta)
	endpoint := fmt.Sprintf("%s://%s.%s.%s.svc:%d", h.getScheme(etcd), survivingPodName, peerSvcName, etcd.Namespace, clientPort)

	// Build member identifiers in format "name=peerURL"
	var members []string
	for _, m := range h.config.MembersToRemove {
		members = append(members, fmt.Sprintf("%s=%s", m.Name, m.PeerURL))
	}

	args := []string{
		"member-remove",
		fmt.Sprintf("--members=%s", strings.Join(members, ",")),
		fmt.Sprintf("--endpoints=%s", endpoint),
	}

	if etcd.Spec.Etcd.ClientUrlTLS != nil {
		args = append(args,
			fmt.Sprintf("--cacert=%s/bundle.crt", common.VolumeMountPathEtcdCA),
			fmt.Sprintf("--cert=%s/tls.crt", common.VolumeMountPathEtcdClientTLS),
			fmt.Sprintf("--key=%s/tls.key", common.VolumeMountPathEtcdClientTLS),
		)
	} else {
		args = append(args, "--insecure-transport=true")
	}

	return args
}

// buildTLSVolumesAndMounts returns volumes and volume mounts for etcd client TLS communication.
func (h *handler) buildTLSVolumesAndMounts(etcd *druidv1alpha1.Etcd) ([]corev1.Volume, []corev1.VolumeMount) {
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

// getScheme returns the appropriate URL scheme based on TLS configuration.
func (h *handler) getScheme(etcd *druidv1alpha1.Etcd) string {
	if etcd.Spec.Etcd.ClientUrlTLS != nil {
		return "https"
	}
	return "http"
}
