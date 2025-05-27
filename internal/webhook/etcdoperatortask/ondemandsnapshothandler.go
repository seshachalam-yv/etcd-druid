package etcdoperatortask
import (
	"context"
	"fmt"
	"net/http"	
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (h *Handler) handleOnDemandSnapshot(ctx context.Context, task *druidv1alpha1.EtcdOperatorTask) admission.Response {
	// Ensure the Etcd Object exists and is healthy
	etcd := &druidv1alpha1.Etcd{}
	err := h.client.Get(ctx, client.ObjectKey{
		Name:      task.Spec.EtcdRef.Name,
		Namespace: task.Spec.EtcdRef.Namespace,
	}, etcd)
	// Check if the Etcd object was found
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			h.logger.Error(err, "Error fetching referenced Etcd")
			return admission.Errored(http.StatusBadRequest, err)
		}
		h.logger.Info("Referenced Etcd not found", "namespace", task.Spec.EtcdRef.Namespace, "name", task.Spec.EtcdRef.Name)
		return admission.Denied("etcd cluster referenced in spec.etcdRef does not exist")
	}
	// Check if Etcd is healthy
	if err := CheckEtcdReadiness(ctx, etcd); err != nil {
		h.logger.Info("Etcd is not ready", "namespace", task.Spec.EtcdRef.Namespace, "name", task.Spec.EtcdRef.Name)
		return admission.Denied("etcd cluster referenced in spec.etcdRef is not ready: " + err.Error())
	}
	//Check if the backup is enabled
	if !etcd.IsBackupStoreEnabled() {
		h.logger.Info("Backup is not enabled for etcd", "namespace", task.Spec.EtcdRef.Namespace, "name", task.Spec.EtcdRef.Name)
		return admission.Denied("backup is not enabled for etcd cluster referenced in spec.etcdRef")
	}
	// if spec.config.OnDemandSnapshot.type is delta then spec.confg.OnDemandSnapshot.isFInal cant be true
	if (task.Spec.Config.OnDemandSnapshot.Type == druidv1alpha1.OnDemandSnapshotTypeDelta) && *task.Spec.Config.OnDemandSnapshot.IsFinal {
		h.logger.Info("Invalid OnDemandSnapshot config", "namespace", task.Spec.EtcdRef.Namespace, "name", task.Spec.EtcdRef.Name)
		return admission.Denied("spec.config.onDemandSnapshotConfig.type is delta, so spec.config.onDemandSnapshotConfig.isFinal cannot be true")
	}
	// Duplicate CR check
	var etcdoperatortaskList druidv1alpha1.EtcdOperatorTaskList
	err = h.client.List(ctx, &etcdoperatortaskList, client.InNamespace(task.Namespace))
	if err != nil {
		h.logger.Error(err, "Error fetching EtcdOperatorTask list")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	// Duplicate CR check: ensure no other task (except this one) has the same etcdRef and OnDemandSnapshot config (non-nil)
	for _, existingTask := range etcdoperatortaskList.Items {
		if existingTask.Name == task.Name && existingTask.Namespace == task.Namespace {
			continue // skip the current task itself
		}
		if existingTask.Spec.EtcdRef != nil && task.Spec.EtcdRef != nil &&
			existingTask.Spec.EtcdRef.Name == task.Spec.EtcdRef.Name &&
			existingTask.Spec.EtcdRef.Namespace == task.Spec.EtcdRef.Namespace &&
			existingTask.Spec.Config.OnDemandSnapshot != nil {
			return admission.Denied("another EtcdOperatorTask with the same etcdRef and OnDemandSnapshot config already exists")
		}
	}
	return admission.Allowed("OnDemandSnapshot config valid")

}

// CheckEtcdReadiness checks if the etcd resource is ready
func CheckEtcdReadiness(ctx context.Context, etcd *druidv1alpha1.Etcd) error {
	for _, condition := range etcd.Status.Conditions {
		if condition.Type == druidv1alpha1.ConditionTypeReady {
			if condition.Status == druidv1alpha1.ConditionTrue {
				return nil
			}
			return fmt.Errorf("etcd is not ready: %s", condition.Message)
		}
	}
	return fmt.Errorf("etcd is not ready")
}