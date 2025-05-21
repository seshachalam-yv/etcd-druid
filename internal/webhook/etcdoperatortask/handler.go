// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package etcdoperatortask

import (
	context "context"

	"log"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

const (
	webhookPath = "/webhooks/etcdoperatortask"
)

type Handler struct {
	client  client.Client
	decoder admission.Decoder
}

func NewHandler(mgr manager.Manager) *Handler {
	return &Handler{
		client:  mgr.GetClient(),
		decoder: admission.NewDecoder(mgr.GetScheme()),
	}
}

func (h *Handler) Handle(ctx context.Context, req admission.Request) admission.Response {
	log.Printf("[EtcdOperatorTask Webhook] Handling %s operation for %s/%s", req.Operation, req.Namespace, req.Name)
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[EtcdOperatorTask Webhook] PANIC recovered in Handle: %v", r)
			log.Printf("[EtcdOperatorTask Webhook] Request: operation=%s, namespace=%s, name=%s, kind=%v", req.Operation, req.Namespace, req.Name, req.Kind)
		}
	}()
	// Only handle EtcdOperatorTask
	if req.Kind.Kind != "EtcdOperatorTask" {
		log.Printf("[EtcdOperatorTask Webhook] Skipping non-EtcdOperatorTask resource: %s", req.Kind.Kind)
		return admission.Allowed("not an EtcdOperatorTask resource")
	}

	switch req.Operation {
	case "CREATE":
		return h.validateCreate(ctx, req)
	case "UPDATE":
		return h.validateUpdate(ctx, req)
	case "DELETE":
		log.Printf("[EtcdOperatorTask Webhook] Allowing DELETE for %s/%s", req.Namespace, req.Name)
		return admission.Allowed("delete always allowed")
	default:
		log.Printf("[EtcdOperatorTask Webhook] Operation %s not handled for %s/%s", req.Operation, req.Namespace, req.Name)
		return admission.Allowed("operation not handled")
	}
}

func (h *Handler) validateCreate(ctx context.Context, req admission.Request) admission.Response {
	log.Printf("[EtcdOperatorTask Webhook] Entering validateCreate for %s/%s", req.Namespace, req.Name)
	obj := &druidv1alpha1.EtcdOperatorTask{}
	log.Printf("[EtcdOperatorTask Webhook] About to decode object for %s/%s", req.Namespace, req.Name)
	if err := h.decoder.Decode(req, obj); err != nil {
		log.Printf("[EtcdOperatorTask Webhook] Failed to decode object: %v (line: validateCreate:Decode)", err)
		return admission.Errored(400, err)
	}
	log.Printf("[EtcdOperatorTask Webhook] Decoded object: %+v (line: validateCreate:afterDecode)", obj)
	// Defensive: check obj.Spec is not nil (should always be present, but just in case)
	log.Printf("[EtcdOperatorTask Webhook] Checking if obj.Spec is empty for %s/%s", req.Namespace, req.Name)
	if obj.Spec == (druidv1alpha1.EtcdOperatorTaskSpec{}) {
		log.Printf("[EtcdOperatorTask Webhook] Missing spec in object: %s/%s (line: validateCreate:specNil)", req.Namespace, req.Name)
		return admission.Denied("spec is required")
	}

	// Basic required field checks
	log.Printf("[EtcdOperatorTask Webhook] Checking etcdRef for %s/%s", req.Namespace, req.Name)
	if obj.Spec.EtcdRef == nil {
		log.Printf("[EtcdOperatorTask Webhook] etcdRef is nil in %s/%s (line: validateCreate:etcdRefNil)", req.Namespace, req.Name)
		return admission.Denied("spec.etcdRef.name and spec.etcdRef.namespace are required")
	}
	log.Printf("[EtcdOperatorTask Webhook] etcdRef: %+v", obj.Spec.EtcdRef)
	if obj.Spec.EtcdRef.Name == "" {
		log.Printf("[EtcdOperatorTask Webhook] etcdRef.Name is empty in %s/%s (line: validateCreate:etcdRefNameEmpty)", req.Namespace, req.Name)
		return admission.Denied("spec.etcdRef.name and spec.etcdRef.namespace are required")
	}
	if obj.Spec.EtcdRef.Namespace == "" {
		log.Printf("[EtcdOperatorTask Webhook] etcdRef.Namespace is empty in %s/%s (line: validateCreate:etcdRefNamespaceEmpty)", req.Namespace, req.Name)
		return admission.Denied("spec.etcdRef.name and spec.etcdRef.namespace are required")
	}
	log.Printf("[EtcdOperatorTask Webhook] Checking onDemandSnapshotConfig for %s/%s", req.Namespace, req.Name)
	if obj.Spec.Config.OnDemandSnapshot == nil {
		log.Printf("[EtcdOperatorTask Webhook] Missing onDemandSnapshotConfig in %s/%s (line: validateCreate:onDemandSnapshotNil)", req.Namespace, req.Name)
		return admission.Denied("spec.config.onDemandSnapshotConfig is required")
	}
	log.Printf("[EtcdOperatorTask Webhook] Checking TTLSecondsAfterFinished for %s/%s", req.Namespace, req.Name)
	if obj.Spec.TTLSecondsAfterFinished != nil {
		log.Printf("[EtcdOperatorTask Webhook] TTLSecondsAfterFinished value: %v", *obj.Spec.TTLSecondsAfterFinished)
		if *obj.Spec.TTLSecondsAfterFinished <= 0 {
			log.Printf("[EtcdOperatorTask Webhook] Invalid ttlSecondsAfterFinished in %s/%s: %v (line: validateCreate:ttlInvalid)", req.Namespace, req.Name, *obj.Spec.TTLSecondsAfterFinished)
			return admission.Denied("spec.ttlSecondsAfterFinished must be greater than zero if set")
		}
	}
	// check corresponding EtcdCluster exists
	log.Printf("[EtcdOperatorTask Webhook] About to fetch referenced Etcd for %s/%s: %s/%s", req.Namespace, req.Name, obj.Spec.EtcdRef.Namespace, obj.Spec.EtcdRef.Name)
	etcd := &druidv1alpha1.Etcd{}
	if err := h.client.Get(ctx, client.ObjectKey{
		Name:      obj.Spec.EtcdRef.Name,
		Namespace: obj.Spec.EtcdRef.Namespace,
	}, etcd); err != nil {
		log.Printf("[EtcdOperatorTask Webhook] client.Get returned error: %v", err)
		if client.IgnoreNotFound(err) != nil {
			log.Printf("[EtcdOperatorTask Webhook] Error fetching referenced Etcd: %v (line: validateCreate:clientGet)", err)
			return admission.Errored(400, err)
		}
		log.Printf("[EtcdOperatorTask Webhook] Referenced Etcd not found: %s/%s (line: validateCreate:etcdNotFound)", obj.Spec.EtcdRef.Namespace, obj.Spec.EtcdRef.Name)
		return admission.Denied("etcd cluster referenced in spec.etcdRef does not exist")
	}
	log.Printf("[EtcdOperatorTask Webhook] Successfully fetched referenced Etcd: %+v", etcd)
	log.Printf("[EtcdOperatorTask Webhook] Create validated for %s/%s (line: validateCreate:success)", req.Namespace, req.Name)
	return admission.Allowed("EtcdOperatorTask create validated")
}

func (h *Handler) validateUpdate(ctx context.Context, req admission.Request) admission.Response {
	log.Printf("[EtcdOperatorTask Webhook] Entering validateUpdate for %s/%s", req.Namespace, req.Name)
	oldObj := &druidv1alpha1.EtcdOperatorTask{}
	newObj := &druidv1alpha1.EtcdOperatorTask{}
	// Decode new object
	if err := h.decoder.Decode(req, newObj); err != nil {
		log.Printf("[EtcdOperatorTask Webhook] Failed to decode new object for update: %v (line: validateUpdate:newDecode)", err)
		return admission.Errored(400, err)
	}
	log.Printf("[EtcdOperatorTask Webhook] Decoded new object: %+v (line: validateUpdate:afterNewDecode)", newObj)
	// Decode old object by creating a new admission.Request with OldObject as Object
	oldReq := req
	oldReq.Object = req.OldObject
	if err := h.decoder.Decode(oldReq, oldObj); err != nil {
		log.Printf("[EtcdOperatorTask Webhook] Failed to decode old object for update: %v (line: validateUpdate:oldDecode)", err)
		return admission.Errored(400, err)
	}
	log.Printf("[EtcdOperatorTask Webhook] Decoded old object: %+v (line: validateUpdate:afterOldDecode)", oldObj)
	// Spec is immutable
	if !equality.Semantic.DeepEqual(oldObj.Spec, newObj.Spec) {
		log.Printf("[EtcdOperatorTask Webhook] Spec is immutable: attempted update for %s/%s (line: validateUpdate:specImmutable)", req.Namespace, req.Name)
		return admission.Denied("spec is immutable after creation")
	}
	log.Printf("[EtcdOperatorTask Webhook] Update validated for %s/%s (line: validateUpdate:success)", req.Namespace, req.Name)
	return admission.Allowed("EtcdOperatorTask update validated")
}

// InjectDecoder injects the decoder into the Handler.
func (h *Handler) InjectDecoder(d admission.Decoder) error {
	h.decoder = d
	return nil
}

func (h *Handler) RegisterWithManager(mgr manager.Manager) error {
	webhook := &admission.Webhook{
		Handler:      h,
		RecoverPanic: nil,
	}
	mgr.GetWebhookServer().Register(webhookPath, webhook)
	return nil
}
