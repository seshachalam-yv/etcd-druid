// SPDX-FileCopyrightText: 2025 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package statefulset

import (
	"fmt"
	"strings"
	"testing"

	druidconfigv1alpha1 "github.com/gardener/etcd-druid/api/config/v1alpha1"
	druidv1alpha1 "github.com/gardener/etcd-druid/api/core/v1alpha1"
	"github.com/gardener/etcd-druid/internal/common"
	testutils "github.com/gardener/etcd-druid/test/utils"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
)

// buildStsBuilderForEtcd constructs a minimal stsBuilder suitable for testing
// container command args, without a real kube client (client is only used for
// backup-volume resolution, not for args generation).
func buildStsBuilderForEtcd(etcd *druidv1alpha1.Etcd) *stsBuilder {
	iv := testutils.CreateImageVector(true, true)
	b, err := newStsBuilder(nil, logr.Discard(), etcd, etcd.Spec.Replicas, iv, true, buildEmptySTS(etcd))
	if err != nil {
		panic(fmt.Sprintf("buildStsBuilderForEtcd: %v", err))
	}
	return b
}

// TestBackupRestoreContainerCommandArgs verifies that the correct CLI args are
// generated for etcd-backup-restore (gate disabled) and etcd-steward (gate enabled).
func TestBackupRestoreContainerCommandArgs(t *testing.T) {
	testCases := []struct {
		name            string
		useEtcdSteward  bool
		replicas        int32
		withLocalBackup bool
		// args that MUST be present in the output
		mustContain []string
		// args that MUST NOT be present in the output
		mustNotContain []string
	}{
		{
			name:            "backup-restore: gate disabled — legacy args",
			useEtcdSteward:  false,
			replicas:        1,
			withLocalBackup: true,
			mustContain: []string{
				"server",
				fmt.Sprintf("--server-port=%d", common.DefaultPortEtcdBackupRestore),
				"--defragmentation-schedule=",
				"--auto-compaction-mode=",
				"--auto-compaction-retention=",
				"--use-etcd-wrapper=true",
				"--embedded-etcd-quota-bytes=",
			},
			mustNotContain: []string{
				"--defrag-schedule=",
				"--initial-cluster=",
				"--listen-peer-urls=",
				"--listen-client-urls=",
			},
		},
		{
			name:            "etcd-steward: gate enabled — steward args, no legacy flags",
			useEtcdSteward:  true,
			replicas:        3,
			withLocalBackup: true,
			mustContain: []string{
				"server",
				fmt.Sprintf("--server-port=%d", common.DefaultPortEtcdBackupRestore),
				"--defrag-schedule=",
				"--auto-compaction-mode=",
				"--auto-compaction-retention=",
				"--initial-cluster=",
				"--listen-peer-urls=",
				"--listen-client-urls=",
				fmt.Sprintf("--data-dir=%s/new.etcd", common.VolumeMountPathEtcdData),
			},
			mustNotContain: []string{
				"--defragmentation-schedule=",
				"--use-etcd-wrapper=",
				"--embedded-etcd-quota-bytes=",
				"--insecure-transport=",
				"--insecure-skip-tls-verify=",
				"--service-endpoints=",
				"--etcd-connection-timeout-leader-election=",
			},
		},
		{
			name:            "etcd-steward: initial-cluster contains all replicas",
			useEtcdSteward:  true,
			replicas:        3,
			withLocalBackup: false,
			mustContain: []string{
				// Each member name must appear in --initial-cluster
				fmt.Sprintf("--initial-cluster=%s-0=", testutils.TestEtcdName),
			},
		},
		{
			name:            "etcd-steward: no backup store — storage args absent",
			useEtcdSteward:  true,
			replicas:        1,
			withLocalBackup: false,
			mustNotContain: []string{
				"--storage-provider=",
				"--store-prefix=",
			},
		},
		{
			name:            "etcd-steward: with local backup store — storage args present",
			useEtcdSteward:  true,
			replicas:        1,
			withLocalBackup: true,
			mustContain: []string{
				"--storage-provider=Local",
				"--store-prefix=",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			err := druidconfigv1alpha1.DefaultFeatureGates.SetEnabledFeaturesFromMap(
				map[string]bool{druidconfigv1alpha1.UseEtcdSteward: tc.useEtcdSteward},
			)
			g.Expect(err).ToNot(HaveOccurred())
			// Reset gate after test regardless of outcome.
			t.Cleanup(func() {
				_ = druidconfigv1alpha1.DefaultFeatureGates.SetEnabledFeaturesFromMap(
					map[string]bool{druidconfigv1alpha1.UseEtcdSteward: false},
				)
			})

			etcdBuilder := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).
				WithReplicas(tc.replicas)
			if !tc.withLocalBackup {
				etcdBuilder = etcdBuilder.WithoutProvider()
			} else {
				etcdBuilder = etcdBuilder.WithProviderLocal("/tmp").WithoutBackupSecretRef()
			}
			etcd := etcdBuilder.Build()

			b := buildStsBuilderForEtcd(etcd)
			args := b.getBackupRestoreContainerCommandArgs()
			joined := strings.Join(args, " ")

			for _, want := range tc.mustContain {
				g.Expect(joined).To(ContainSubstring(want),
					"expected arg %q to be present in: %s", want, joined)
			}
			for _, notWant := range tc.mustNotContain {
				g.Expect(joined).ToNot(ContainSubstring(notWant),
					"expected arg %q to be absent in: %s", notWant, joined)
			}

			// For steward + 3 replicas: verify all 3 member names in --initial-cluster
			if tc.useEtcdSteward && tc.replicas == 3 {
				for i := range 3 {
					podName := druidv1alpha1.GetOrdinalPodName(etcd.ObjectMeta, i)
					g.Expect(joined).To(ContainSubstring(podName+"="),
						"expected member %q in --initial-cluster", podName)
				}
			}
		})
	}
}

// TestBackupRestoreArgsGateOffNoRegression verifies that enabling and then
// disabling UseEtcdSteward produces identical args to never having it enabled.
func TestBackupRestoreArgsGateOffNoRegression(t *testing.T) {
	g := NewWithT(t)

	etcd := testutils.EtcdBuilderWithDefaults(testutils.TestEtcdName, testutils.TestNamespace).
		WithReplicas(1).
		WithProviderLocal("/tmp").
		WithoutBackupSecretRef().
		Build()

	// Gate disabled — baseline
	err := druidconfigv1alpha1.DefaultFeatureGates.SetEnabledFeaturesFromMap(
		map[string]bool{druidconfigv1alpha1.UseEtcdSteward: false},
	)
	g.Expect(err).ToNot(HaveOccurred())
	baseline := buildStsBuilderForEtcd(etcd).getBackupRestoreContainerCommandArgs()

	// Enable gate, then disable again — must be identical to baseline
	err = druidconfigv1alpha1.DefaultFeatureGates.SetEnabledFeaturesFromMap(
		map[string]bool{druidconfigv1alpha1.UseEtcdSteward: true},
	)
	g.Expect(err).ToNot(HaveOccurred())
	_ = buildStsBuilderForEtcd(etcd).getBackupRestoreContainerCommandArgs()

	err = druidconfigv1alpha1.DefaultFeatureGates.SetEnabledFeaturesFromMap(
		map[string]bool{druidconfigv1alpha1.UseEtcdSteward: false},
	)
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() {
		_ = druidconfigv1alpha1.DefaultFeatureGates.SetEnabledFeaturesFromMap(
			map[string]bool{druidconfigv1alpha1.UseEtcdSteward: false},
		)
	})
	after := buildStsBuilderForEtcd(etcd).getBackupRestoreContainerCommandArgs()

	g.Expect(after).To(Equal(baseline), "args with gate off must match baseline after toggle")
}

// buildEmptySTS returns a minimal existing StatefulSet skeleton (used to
// satisfy the stsBuilder constructor's existing-sts parameter).
func buildEmptySTS(etcd *druidv1alpha1.Etcd) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      etcd.Name,
			Namespace: etcd.Namespace,
		},
	}
}
