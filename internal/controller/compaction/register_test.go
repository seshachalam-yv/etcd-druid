// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package compaction

import (
	"crypto/rand"
	"math/big"
	"strconv"
	"testing"
	"time"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/test/utils"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	. "github.com/onsi/gomega"
)

func TestEtcdMemberSnapshotsChangedForCreateEvents(t *testing.T) {
	g := NewWithT(t)
	t.Parallel()
	p := etcdMemberSnapshotsChanged()
	member := &druidv1alpha1.EtcdMember{
		ObjectMeta: metav1.ObjectMeta{Name: "test-member"},
	}
	g.Expect(p.Create(event.CreateEvent{Object: member})).To(BeFalse())
}

func TestEtcdMemberSnapshotsChangedForDeleteEvents(t *testing.T) {
	g := NewWithT(t)
	t.Parallel()
	p := etcdMemberSnapshotsChanged()
	member := &druidv1alpha1.EtcdMember{
		ObjectMeta: metav1.ObjectMeta{Name: "test-member"},
	}
	g.Expect(p.Delete(event.DeleteEvent{Object: member})).To(BeFalse())
}

func TestEtcdMemberSnapshotsChangedForGenericEvents(t *testing.T) {
	g := NewWithT(t)
	t.Parallel()
	p := etcdMemberSnapshotsChanged()
	member := &druidv1alpha1.EtcdMember{
		ObjectMeta: metav1.ObjectMeta{Name: "test-member"},
	}
	g.Expect(p.Generic(event.GenericEvent{Object: member})).To(BeFalse())
}

func TestEtcdMemberSnapshotsChangedForUpdateEvents(t *testing.T) {
	qty100 := resource.MustParse("100Mi")
	tests := []struct {
		name                string
		oldSnapshots        *druidv1alpha1.EtcdMemberSnapshots
		newSnapshots        *druidv1alpha1.EtcdMemberSnapshots
		expectedAllowUpdate bool
	}{
		{
			name:                "both nil — no change",
			oldSnapshots:        nil,
			newSnapshots:        nil,
			expectedAllowUpdate: false,
		},
		{
			name:         "snapshots added — nil to non-nil",
			oldSnapshots: nil,
			newSnapshots: &druidv1alpha1.EtcdMemberSnapshots{
				LastFull: &druidv1alpha1.EtcdMemberSnapshotInfo{EndRevision: 100},
			},
			expectedAllowUpdate: true,
		},
		{
			name: "LastFull EndRevision changed",
			oldSnapshots: &druidv1alpha1.EtcdMemberSnapshots{
				LastFull: &druidv1alpha1.EtcdMemberSnapshotInfo{EndRevision: 100},
			},
			newSnapshots: &druidv1alpha1.EtcdMemberSnapshots{
				LastFull: &druidv1alpha1.EtcdMemberSnapshotInfo{EndRevision: 200},
			},
			expectedAllowUpdate: true,
		},
		{
			name: "AccumulatedDeltaSize changed",
			oldSnapshots: &druidv1alpha1.EtcdMemberSnapshots{
				LastFull:             &druidv1alpha1.EtcdMemberSnapshotInfo{EndRevision: 100},
				AccumulatedDeltaSize: &qty100,
			},
			newSnapshots: &druidv1alpha1.EtcdMemberSnapshots{
				LastFull:             &druidv1alpha1.EtcdMemberSnapshotInfo{EndRevision: 100},
				AccumulatedDeltaSize: ptr.To(resource.MustParse("200Mi")),
			},
			expectedAllowUpdate: true,
		},
		{
			name: "no change to snapshots",
			oldSnapshots: &druidv1alpha1.EtcdMemberSnapshots{
				LastFull: &druidv1alpha1.EtcdMemberSnapshotInfo{EndRevision: 100},
			},
			newSnapshots: &druidv1alpha1.EtcdMemberSnapshots{
				LastFull: &druidv1alpha1.EtcdMemberSnapshotInfo{EndRevision: 100},
			},
			expectedAllowUpdate: false,
		},
		{
			name:                "object is not an EtcdMember — returns false",
			oldSnapshots:        nil,
			newSnapshots:        nil,
			expectedAllowUpdate: false,
		},
	}

	g := NewWithT(t)
	t.Parallel()
	p := etcdMemberSnapshotsChanged()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			oldMember := &druidv1alpha1.EtcdMember{
				ObjectMeta: metav1.ObjectMeta{Name: "test-member"},
				Status:     druidv1alpha1.EtcdMemberResourceStatus{Snapshots: tc.oldSnapshots},
			}
			newMember := &druidv1alpha1.EtcdMember{
				ObjectMeta: metav1.ObjectMeta{Name: "test-member"},
				Status:     druidv1alpha1.EtcdMemberResourceStatus{Snapshots: tc.newSnapshots},
			}
			g.Expect(p.Update(event.UpdateEvent{ObjectOld: oldMember, ObjectNew: newMember})).To(Equal(tc.expectedAllowUpdate))
		})
	}
}

func TestJobStatusChangedForUpdateEvents(t *testing.T) {
	tests := []struct {
		name                   string
		isObjectJob            bool
		isObjectCompactionJob  bool
		isStatusChanged        bool
		shouldAllowUpdateEvent bool
	}{
		{
			name:                   "object is not a job",
			isObjectJob:            false,
			isObjectCompactionJob:  false,
			shouldAllowUpdateEvent: false,
		},
		{
			name:                   "object is a non-compaction job, and status is not changed",
			isObjectJob:            true,
			isObjectCompactionJob:  false,
			isStatusChanged:        false,
			shouldAllowUpdateEvent: false,
		},
		{
			name:                   "object is a non-compaction job, and status is changed",
			isObjectJob:            true,
			isObjectCompactionJob:  false,
			isStatusChanged:        true,
			shouldAllowUpdateEvent: false,
		},
		{
			name:                   "object is a compaction job, but status is not changed",
			isObjectJob:            true,
			isObjectCompactionJob:  true,
			isStatusChanged:        false,
			shouldAllowUpdateEvent: false,
		},
		{
			name:                   "object is a compaction job, and status is changed",
			isObjectJob:            true,
			isObjectCompactionJob:  true,
			isStatusChanged:        true,
			shouldAllowUpdateEvent: true,
		},
	}

	g := NewWithT(t)
	t.Parallel()
	predicate := compactionJobStatusChanged()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			obj, oldObj := createObjectsForJobStatusChangedPredicate(g, druidv1alpha1.GetCompactionJobName(metav1.ObjectMeta{Name: utils.TestEtcdName}), test.isObjectJob, test.isObjectCompactionJob, test.isStatusChanged)
			g.Expect(predicate.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: obj})).To(Equal(test.shouldAllowUpdateEvent))
		})
	}
}

func createObjectsForJobStatusChangedPredicate(g *WithT, name string, isJobObj, isCompactionJob, isStatusChanged bool) (obj client.Object, oldObj client.Object) {
	// if the object is not a job object, create a config map (random type chosen, could have been anything else as well).
	if !isJobObj {
		obj = createConfigMap(g, name)
		oldObj = createConfigMap(g, name)
		return
	}
	// If the object is a job but not a compaction job, create a regular job
	if !isCompactionJob {
		obj = createNonCompactionJob(g, name)
		oldObj = createNonCompactionJob(g, name)
		return
	}

	now := time.Now()

	etcdName := utils.TestEtcdName
	etcdKind := druidv1alpha1.SchemeGroupVersion.WithKind("Etcd").Kind

	// Create proper owner reference for compaction job
	ownerRef := metav1.OwnerReference{
		APIVersion:         druidv1alpha1.SchemeGroupVersion.String(),
		Kind:               etcdKind,
		Name:               etcdName,
		UID:                "test-etcd-uid-12345",
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}

	// create job objects
	oldObj = &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       utils.TestNamespace,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Status: batchv1.JobStatus{
			Active: 1,
			StartTime: &metav1.Time{
				Time: now,
			},
		},
	}
	if isStatusChanged {
		obj = &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       utils.TestNamespace,
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Status: batchv1.JobStatus{
				Succeeded: 1,
				StartTime: &metav1.Time{
					Time: now,
				},
				CompletionTime: &metav1.Time{
					Time: time.Now(),
				},
			},
		}
	} else {
		obj = oldObj
	}
	return
}

func createConfigMap(g *WithT, name string) *corev1.ConfigMap {
	randInt := generateRandomInt(g)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Data: map[string]string{
			"k": strconv.Itoa(randInt),
		},
	}
}

func createNonCompactionJob(g *WithT, name string) *batchv1.Job {
	randInt := generateRandomInt(g)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: utils.TestNamespace,
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: ptr.To(int64(randInt)),
		},
		Status: batchv1.JobStatus{
			Active: 1,
		},
	}
}

func generateRandomInt(g *WithT) int {
	randInt, err := rand.Int(rand.Reader, big.NewInt(1000))
	g.Expect(err).NotTo(HaveOccurred())
	return int(randInt.Int64())
}
