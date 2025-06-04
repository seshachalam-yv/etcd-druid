// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdopstaskprotection

import (
	context "context"
	"net/http"
    druidconfigv1alpha1 "github.com/gardener/etcd-druid/api/config/v1alpha1"
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type Handler struct {
	client  client.Client
	decoder admission.Decoder
	config  druidconfigv1alpha1.EtcdOpsTaskWebhookConfiguration
	logger  logr.Logger
}

func NewHandler(mgr manager.Manager, config druidconfigv1alpha1.EtcdOpsTaskWebhookConfiguration) (*Handler, error) {
	return &Handler{
		client:  mgr.GetClient(),
		decoder: admission.NewDecoder(mgr.GetScheme()),
		config:  config,
		logger:  mgr.GetLogger().WithName("etcdopstask-webhook"),
	}, nil
}

func (h *Handler) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := h.logger.WithValues("name", req.Name, "namespace", req.Namespace, "operation", req.Operation, "user", req.UserInfo.Username)

	var task druidv1alpha1.EtcdOpsTask
	if err := h.decoder.Decode(req, &task); err != nil {
		log.Error(err, "failed to decode request")
		return admission.Errored(http.StatusBadRequest, err)
	}
	var (
		configSet string
		count     int
	)

	if task.Spec.Config.OnDemandSnapshot != nil {
		configSet = "OnDemandSnapshot"
		count++
	}
	// Add more config checks as needed and increment count

	if count == 0 {
		return admission.Denied("spec.config must have exactly one config field set, but none were found")
	}
	if count > 1 {
		return admission.Denied("spec.config must have exactly one config field set, but multiple were found")
	}

	// Call the appropriate handler
	switch configSet {
	case "OnDemandSnapshot":
		return h.handleOnDemandSnapshot(ctx, &task)
	// Add more cases as needed
	default:
		return admission.Denied("No handler implemented for config: " + configSet)
	}
}
