// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdoperatortask

import (
	context "context"
	"net/http"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type Handler struct {
	client  client.Client
	decoder admission.Decoder
	config  *Config
	logger  logr.Logger
}

func NewHandler(mgr manager.Manager, config *Config) (*Handler, error) {
	return &Handler{
		client:  mgr.GetClient(),
		decoder: admission.NewDecoder(mgr.GetScheme()),
		config:  config,
		logger:  mgr.GetLogger().WithName("etcdoperatortask-webhook"),
	}, nil
}

func (h *Handler) Handle(ctx context.Context, req admission.Request) admission.Response {
	// TOdO: Add Serviceacccounts for the
	log := h.logger.WithValues("name", req.Name, "namespace", req.Namespace, "operation", req.Operation, "user", req.UserInfo.Username)
	log.V(1).Info("EtcdOperatorTask webhook invoked")
	if req.Operation == admissionv1.Update {
		log.V(1).Info("Update operation not allowed")
		return admission.Denied("update operation is not allowed")
	}

	if req.Operation == admissionv1.Delete {
		// Check if the user is in the exempted service accounts
		actor := req.UserInfo.Username
		for _, exempt := range h.config.ExemptServiceAccounts {
			if actor == exempt {
				return admission.Allowed("delete allowed for exempt service account")
			}
		}
		return admission.Denied("delete is only allowed for exempt service accounts")
	}

	var task druidv1alpha1.EtcdOperatorTask
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

// InjectDecoder injects the decoder into the Handler.
// func (h *Handler) InjectDecoder(d admission.Decoder) error {
// 	h.decoder = d
// 	return nil
// }
