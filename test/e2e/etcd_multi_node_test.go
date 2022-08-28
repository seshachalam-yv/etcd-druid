package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gardener/etcd-druid/api/v1alpha1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/test/matchers"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s_labels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = FDescribe("Etcd", func() {
	Context("when multi-node is configured", func() {
		var (
			cl               client.Client
			etcdName         string
			storageContainer string
			parentCtx        context.Context
			provider         TestProvider
			providers        []TestProvider
			err              error
		)

		BeforeEach(func() {
			parentCtx = context.Background()
			providers, err = getProviders()
			Expect(err).ToNot(HaveOccurred())
			// take first provider
			provider = providers[0]
			cl, err = getKubernetesClient(kubeconfigPath)
			Expect(err).ShouldNot(HaveOccurred())

			etcdName = fmt.Sprintf("etcd-%s", provider.Name)

			storageContainer = getEnvAndExpectNoError(envStorageContainer)

			snapstoreProvider := provider.Storage.Provider
			store, err := getSnapstore(string(snapstoreProvider), storageContainer, storePrefix)
			Expect(err).ShouldNot(HaveOccurred())

			// purge any existing backups in bucket
			Expect(purgeSnapstore(store)).To(Succeed())
			Expect(deployBackupSecret(parentCtx, cl, logger, provider, etcdNamespace, storageContainer))

		})

		AfterEach(func() {
			// remove etcd objects if any old etcd objects exist.
			ctx, cancelFunc := context.WithTimeout(parentCtx, time.Minute)
			defer cancelFunc()

			purgeEtcd(ctx, cl, providers)
			// for _, p := range providers {
			// 	e := getEmptyEtcd(fmt.Sprintf("etcd-%s", p.Name), namespace)
			// 	if err := cl.Get(ctx, client.ObjectKeyFromObject(e), e); err == nil {
			// 		ExpectWithOffset(1, kutil.DeleteObject(ctx, cl, e)).To(Succeed())

			// 		purgeEtcdPVCs(ctx, cl, e.Name)
			// 		// r, _ := k8s_labels.NewRequirement("instance", selection.Equals, []string{e.Name})
			// 		// opts := &client.ListOptions{LabelSelector: k8s_labels.NewSelector().Add(*r)}
			// 		// pvcList := &corev1.PersistentVolumeClaimList{}
			// 		// cl.List(ctx, pvcList, client.InNamespace(namespace), opts)
			// 		// for _, pvc := range pvcList.Items {
			// 		// 	ExpectWithOffset(1, kutil.DeleteObject(ctx, cl, &pvc)).To(Succeed())
			// 		// }
			// 	}

			// }
		})
		FIt("should perform etcd operations", func() {
			ctx, cancelFunc := context.WithTimeout(parentCtx, 10*time.Minute)
			defer cancelFunc()

			etcd := getDefaultMultiNodeEtcd(etcdName, namespace, storageContainer, storePrefix, provider)
			objLogger := logger.WithValues("etcd-multi-node", client.ObjectKeyFromObject(etcd))

			By("Create etcd")
			createAndCheckEtcd(ctx, cl, objLogger, etcd)

			// By("Hibernate etcd (Scale down from 3->0)")
			// hibernateAndCheckEtcd(ctx, cl, objLogger, etcd)

			// By("Wakeup etcd (Scale up from 0->3)")
			// wakeupEtcd(ctx, cl, objLogger, etcd)

			By("Zero downtime rolling updates")
			job := startEtcdZeroDownTimeValidatorJob(ctx, cl, etcd, "rolling-update")
			defer cleanUpTestHelperJob(ctx, cl, client.ObjectKeyFromObject(job)) // this defer ensures to remove the job, if test case breaks before deleting the job.

			ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)).To(Succeed())
			// trigger rolling update by updating etcd quota
			etcd.Spec.Etcd.Quota.Add(*resource.NewMilliQuantity(int64(10), resource.DecimalSI))
			updateAndCheckEtcd(ctx, cl, objLogger, etcd)
			checkEtcdZeroDownTimeValidatorJob(ctx, cl, client.ObjectKeyFromObject(job), objLogger)
			objLogger.Info("before cleanUpTestHelperJob")
			cleanUpTestHelperJob(ctx, cl, client.ObjectKeyFromObject(job)) // remove job
			objLogger.Info("after cleanUpTestHelperJob")

			By("Zero downtime maintenance operation: defragmentation")
			job = startEtcdZeroDownTimeValidatorJob(ctx, cl, etcd, "defragmentation")
			defer cleanUpTestHelperJob(ctx, cl, client.ObjectKeyFromObject(job)) // this defer ensures to remove the job, if test case breaks before deleting the job.

			objLogger.Info("Configure defragmentation schedule for every 1 minute")
			ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)).To(Succeed())
			*etcd.Spec.Etcd.DefragmentationSchedule = "*/1 * * * *"
			updateAndCheckEtcd(ctx, cl, objLogger, etcd)

			checkDefragmentationFinished(ctx, cl, etcd, objLogger)

			objLogger.Info("Checking any Etcd downtime")
			checkEtcdZeroDownTimeValidatorJob(ctx, cl, client.ObjectKeyFromObject(job), objLogger)
			cleanUpTestHelperJob(ctx, cl, client.ObjectKeyFromObject(job))

			By("Delete etcd")
			deleteAndCheckEtcd(ctx, cl, objLogger, etcd)
		})
	})

	Context("when single-node is configured", func() {
		var (
			cl               client.Client
			etcdName         string
			storageContainer string
			parentCtx        context.Context
			provider         TestProvider
			providers        []TestProvider
			err              error
		)

		BeforeEach(func() {
			parentCtx = context.Background()
			providers, err = getProviders()
			Expect(err).ToNot(HaveOccurred())
			// take first provider
			provider = providers[0]
			provider.Storage = nil

			cl, err = getKubernetesClient(kubeconfigPath)
			Expect(err).ShouldNot(HaveOccurred())

			etcdName = fmt.Sprintf("etcd-%s", provider.Name)
			typedClient, err = getKubernetesTypedClient(kubeconfigPath)
			Expect(err).NotTo(HaveOccurred())

		})

		AfterEach(func() {
			// remove etcd objects if any old etcd objects exist.
			ctx, cancelFunc := context.WithTimeout(parentCtx, time.Minute)
			defer cancelFunc()

			purgeEtcd(ctx, cl, providers)
		})

		It("should scale single-node etcd to multi-node etcd cluster", func() {
			ctx, cancelFunc := context.WithTimeout(parentCtx, 10*time.Minute)
			defer cancelFunc()

			etcd := getDefaultEtcd(etcdName, namespace, storageContainer, storePrefix, provider)
			objLogger := logger.WithValues("etcd-multi-node", client.ObjectKeyFromObject(etcd))

			By("Create single-node etcd")
			createAndCheckEtcd(ctx, cl, objLogger, etcd)

			By("Scale up of a healthy cluster (from 1->3)")
			ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)).To(Succeed())
			etcd.Spec.Replicas = multiNodeEtcdReplicas
			updateAndCheckEtcd(ctx, cl, objLogger, etcd)

			// TODO: Uncomment me once scale down replicas from 3 to 1 is supported.
			// By("Scale down of a healthy cluster (from 3 to 1)")
			// ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)).To(Succeed())
			// etcd.Spec.Replicas = replicas
			// updateAndCheckEtcd(ctx, cl, objLogger, etcd)

			By("Delete single-node etcd")
			deleteAndCheckEtcd(ctx, cl, objLogger, etcd)
		})
	})
})

// checkEventuallyEtcdRollingUpdateDone is a helper function, uses Gomega Eventually.
// Returns the function until etcd rolling updates is done for given timeout and polling interval or
// it raises assertion error.
//
// checkEventuallyEtcdRollingUpdateDone ensures rolling updates of etcd resources(etcd/sts) is done.
func checkEventuallyEtcdRollingUpdateDone(ctx context.Context, cl client.Client, logger logr.Logger, etcd *v1alpha1.Etcd,
	oldStsObservedGeneration, oldEtcdObservedGeneration int64) {
	Eventually(func() error {
		sts := &appsv1.StatefulSet{}
		ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), sts)).To(Succeed())

		if sts.Status.ObservedGeneration <= oldStsObservedGeneration {
			return fmt.Errorf("waiting for statefulset rolling update to complete %d pods at revision %s",
				sts.Status.UpdatedReplicas, sts.Status.UpdateRevision)
		}

		if sts.Status.UpdatedReplicas != *sts.Spec.Replicas {
			return fmt.Errorf("waiting for statefulset rolling update to complete, UpdatedReplicas is %d, but expected to be %d",
				sts.Status.UpdatedReplicas, *sts.Spec.Replicas)
		}

		ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)).To(Succeed())
		if *etcd.Status.ObservedGeneration <= oldEtcdObservedGeneration {
			return fmt.Errorf("waiting for etcd %q rolling update to complete", etcd.Name)
		}
		return nil
	}, timeout*3, pollingInterval).Should(BeNil())
}

// hibernateAndCheckEtcd scales down etcd replicas to zero and ensures
func hibernateAndCheckEtcd(ctx context.Context, cl client.Client, logger logr.Logger, etcd *v1alpha1.Etcd) {
	ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)).To(Succeed())
	etcd.Spec.Replicas = 0
	etcd.SetAnnotations(
		map[string]string{
			v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationReconcile,
		})
	ExpectWithOffset(1, cl.Update(ctx, etcd)).ShouldNot(HaveOccurred())
	logger.Info("Waiting to hibernate")

	logger.Info("Checking statefulset")
	Eventually(func() error {
		sts := &appsv1.StatefulSet{}
		ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), sts)).To(Succeed())

		if sts.Status.ReadyReplicas != 0 {
			return fmt.Errorf("sts %s not ready", etcd.Name)
		}
		return nil
	}, timeout*3, pollingInterval).Should(BeNil())

	logger.Info("Checking etcd")
	Eventually(func() error {
		etcd := getEmptyEtcd(etcd.Name, namespace)
		err := cl.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)
		if err != nil {
			return err
		}

		if etcd != nil && etcd.Status.Ready != nil {
			if *etcd.Status.Ready != true {
				return fmt.Errorf("etcd %s is not ready", etcd.Name)
			}
		}

		if etcd.Status.ClusterSize == nil {
			return fmt.Errorf("etcd %s cluster size is empty", etcd.Name)
		}
		// TODO: uncomment me once scale down is supported,
		// currently ClusterSize is not updated while scaling down.
		// if *etcd.Status.ClusterSize != 0 {
		// 	return fmt.Errorf("etcd %q cluster size is %d, but expected to be 0",
		// 		etcdName, *etcd.Status.ClusterSize)
		// }

		for _, c := range etcd.Status.Conditions {
			if c.Status != v1alpha1.ConditionUnknown {
				return fmt.Errorf("etcd %s status condition is %q, but expected to be %s ",
					etcd.Name, c.Status, v1alpha1.ConditionUnknown)
			}
		}

		return nil
	}, timeout*3, pollingInterval).Should(BeNil())
	logger.Info("etcd is hibernated")
}

// wakeupEtcd scales up etcd replicas to 3 and ensures etcd cluster with 3 replicas is ready.
func wakeupEtcd(ctx context.Context, cl client.Client, logger logr.Logger, etcd *v1alpha1.Etcd) {
	ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)).To(Succeed())
	etcd.Spec.Replicas = multiNodeEtcdReplicas
	updateAndCheckEtcd(ctx, cl, logger, etcd)
}

// updateAndCheckEtcd updates the given etcd obj in the Kubernetes cluster.
func updateAndCheckEtcd(ctx context.Context, cl client.Client, logger logr.Logger, etcd *v1alpha1.Etcd) {
	sts := &appsv1.StatefulSet{}
	ExpectWithOffset(1, cl.Get(ctx, client.ObjectKeyFromObject(etcd), sts)).To(Succeed())
	oldStsObservedGeneration, oldEtcdObservedGeneration := sts.Status.ObservedGeneration, *etcd.Status.ObservedGeneration

	// update reconcile annotation, druid to reconcile and update the changes.
	etcd.SetAnnotations(
		map[string]string{
			v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationReconcile,
		})
	ExpectWithOffset(1, cl.Update(ctx, etcd)).ShouldNot(HaveOccurred())

	// Ensuring update is successful by verifying
	// ObservedGeneration ID of sts, etcd before and after update done.
	checkEventuallyEtcdRollingUpdateDone(ctx, cl, logger, etcd,
		oldStsObservedGeneration, oldEtcdObservedGeneration)
	checkEtcdReady(ctx, cl, logger, etcd)
}

// cleanUpTestHelperJob ensures to remove the given job in the kubernetes cluster if job exists.
func cleanUpTestHelperJob(ctx context.Context, cl client.Client, jobKey types.NamespacedName) {
	job := &batchv1.Job{}
	if err := cl.Get(ctx, jobKey, job); err == nil {
		ExpectWithOffset(1, kutil.DeleteObject(ctx, cl, job)).To(Succeed())
		// r, _ := k8s_labels.NewRequirement("job-name", selection.Equals, []string{job.Name})
		// opts := metav1.ListOptions{
		// 	LabelSelector: k8s_labels.NewSelector().Add(*r),
		// 	Namespace:     namespace,
		// }

		typedClient, _ := getKubernetesTypedClient(kubeconfigPath)

		ExpectWithOffset(1, typedClient.CoreV1().Pods(namespace).DeleteCollection(
			ctx, metav1.DeleteOptions{},
			metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%v",
					job.Name),
			})).ShouldNot(HaveOccurred())
		// podList := &corev1.PodList{}
		// for _, po := range podList.Items {
		// 	client.IgnoreNotFound(cl.Delete(ctx, po, client.PropagationPolicy(metav1.DeletePropagationForeground)))
		// 	// ExpectWithOffset(1, cl.Delete(ctx, po, )).To(Succeed())
		// 	// ExpectWithOffset(1, cl.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace(namespace), client.MatchingLabels{
		// 	// 	"job-name": job.Name,
		// 	// })).To(Succeed())
		// }

	}
}

// checkJobReady checks k8s job associated pod is ready, up and running.
// checkJobReady is a helper function, uses Gomega Eventually.
func checkJobReady(ctx context.Context, cl client.Client, jobName string) {
	r, _ := k8s_labels.NewRequirement("job-name", selection.Equals, []string{jobName})
	opts := &client.ListOptions{
		LabelSelector: k8s_labels.NewSelector().Add(*r),
		Namespace:     namespace,
	}

	Eventually(func() error {
		// job := &batchv1.Job{}
		// ExpectWithOffset(1, cl.Get(ctx, types.NamespacedName{
		// 	Name:      jobName,
		// 	Namespace: namespace},
		// 	job)).ShouldNot(HaveOccurred())

		// if job.Status.Active == int32(0) {
		// 	return fmt.Errorf("job %s is not active", job.Name)
		// }

		podList := &corev1.PodList{}
		ExpectWithOffset(1, cl.List(ctx, podList, opts)).ShouldNot(HaveOccurred())
		if len(podList.Items) == 0 {
			return fmt.Errorf("job %s associated pod is not scheduled", jobName)
		}

		for _, c := range podList.Items[0].Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return nil
			}

		}
		return fmt.Errorf("waiting for pod %v to be ready", podList.Items[0].Name)
	}, timeout, pollingInterval).Should(BeNil())
}

// etcdZeroDownTimeValidatorJob returns k8s job which ensures
// Etcd cluster zero down time by continuously checking etcd cluster health.
// This job fails once health check fails and associated pod results in error status.
func startEtcdZeroDownTimeValidatorJob(ctx context.Context, cl client.Client,
	etcd *v1alpha1.Etcd, testName string) *batchv1.Job {
	job := etcdZeroDownTimeValidatorJob(etcd.Name+"-client", testName, etcd.Spec.Etcd.ClientUrlTLS)

	logger.Info(fmt.Sprintf("Creating job %s to ensure etcd zero downtime", job.Name))
	ExpectWithOffset(1, cl.Create(ctx, job)).ShouldNot(HaveOccurred())

	// Wait until zeroDownTimeValidator job is up and running.
	checkJobReady(ctx, cl, job.Name)
	logger.Info(fmt.Sprintf("Job %s is ready", job.Name))
	return job
}

//getEtcdLeaderPodName returns the leader pod name by using lease
func getEtcdLeaderPodName(ctx context.Context, cl client.Client, namespace string) (*types.NamespacedName, error) {
	leaseList := &v1.LeaseList{}
	opts := &client.ListOptions{Namespace: namespace}
	ExpectWithOffset(1, cl.List(ctx, leaseList, opts)).ShouldNot(HaveOccurred())

	for _, lease := range leaseList.Items {
		if lease.Spec.HolderIdentity == nil {
			return nil, fmt.Errorf("error occurred while finding leader, etcd lease %q spec.holderIdentity is nil", lease.Name)
		}
		if strings.Contains(*lease.Spec.HolderIdentity, "Leader") {
			return &types.NamespacedName{Namespace: namespace, Name: lease.Name}, nil
		}
	}
	return nil, fmt.Errorf("leader doesn't exist for namespace %q", namespace)
}

// getPodLogs returns logs for the given pod
func getPodLogs(ctx context.Context, PodKey *types.NamespacedName, opts *corev1.PodLogOptions) (string, error) {
	typedClient, err := getKubernetesTypedClient(kubeconfigPath)
	if err != nil {
		return "", err
	}

	req := typedClient.CoreV1().Pods(namespace).GetLogs(PodKey.Name, opts)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// checkDefragmentationFinished checks defragmentation is finished or not for given etcd.
func checkDefragmentationFinished(ctx context.Context, cl client.Client, etcd *v1alpha1.Etcd, logger logr.Logger) {
	// Wait until etcd cluster defragmentation is finish.
	logger.Info("Waiting for defragmentation to finish")
	Eventually(func() error {
		leaderPodKey, err := getEtcdLeaderPodName(ctx, cl, namespace)
		if err != nil {
			return err
		}
		// Get etcd leader pod logs to ensure etcd cluster defragmentation is finished or not.
		logs, err := getPodLogs(ctx, leaderPodKey, &corev1.PodLogOptions{
			Container:    "backup-restore",
			SinceSeconds: pointer.Int64(60),
		})

		if err != nil {
			return fmt.Errorf("error occurred while getting [%s] pod logs, error: %v ", leaderPodKey, err)
		}

		for i := 0; i < int(multiNodeEtcdReplicas); i++ {
			if !strings.Contains(logs,
				fmt.Sprintf("Finished defragmenting etcd member[https://%s-%d.%s-peer.%s.svc:2379]",
					etcd.Name, i, etcd.Name, namespace)) {
				return fmt.Errorf("etcd %q defragmentation is not finished for member %q-%d", etcd.Name, etcd.Name, i)
			}
		}

		logger.Info("Defragmentation is finished")
		// Checking Etcd cluster is healthy and there is no downtime while defragmentation.
		// K8s job zeroDownTimeValidator will fail, if there is any downtime in Etcd cluster health.
		return nil
	}, time.Minute*6, pollingInterval).Should(BeNil())

}

// checkEtcdZeroDownTimeValidatorJob ensures etcd cluster health downtime,
// there is no downtime if given job is active.
func checkEtcdZeroDownTimeValidatorJob(ctx context.Context, cl client.Client, jobName types.NamespacedName,
	logger logr.Logger) {

	job := &batchv1.Job{}
	ExpectWithOffset(1, cl.Get(ctx, jobName, job)).To(Succeed())
	Expect(job.Status.Failed).Should(BeZero())
	logger.Info("Etcd Cluster is healthy and there is no downtime")
}

func purgeEtcd(ctx context.Context, cl client.Client, providers []TestProvider) {
	// ExpectWithOffset(1, cl.DeleteAllOf(ctx, &corev1.Pod{})).To(Succeed())
	for _, p := range providers {
		e := getEmptyEtcd(fmt.Sprintf("etcd-%s", p.Name), namespace)
		if err := cl.Get(ctx, client.ObjectKeyFromObject(e), e); err == nil {
			ExpectWithOffset(1, kutil.DeleteObject(ctx, cl, e)).To(Succeed())
			logger.Info("Checking if etcd is gone")
			Eventually(func() error {
				ctx, cancelFunc := context.WithTimeout(ctx, timeout)
				defer cancelFunc()
				return cl.Get(ctx, client.ObjectKeyFromObject(e), e)
			}, timeout*3, pollingInterval).Should(matchers.BeNotFoundError())

			purgeEtcdPVCs(ctx, cl, e.Name)
			// r, _ := k8s_labels.NewRequirement("instance", selection.Equals, []string{e.Name})
			// opts := &client.ListOptions{LabelSelector: k8s_labels.NewSelector().Add(*r)}
			// pvcList := &corev1.PersistentVolumeClaimList{}
			// cl.List(ctx, pvcList, client.InNamespace(namespace), opts)
			// for _, pvc := range pvcList.Items {
			// 	ExpectWithOffset(1, kutil.DeleteObject(ctx, cl, &pvc)).To(Succeed())
			// }

		}
	}
}
