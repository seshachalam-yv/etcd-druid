package etcdoperatortask

// TODO:
/*
	1) Define the right kind of metrics for the controller.
	The following data is to be collected:
	- Number of tasks created. Count in such a way that multiple reconcile loops are not overcounted.
	- Number of tasks Successfully run. THis can be done by having a step function at the end of the reconcile. this then updates that counter.
	- Time Taken for each of the phases of the task. triggered whenever there is an update to the lastoperation field in the task at any point. Things to consider:
	    - Admit and Run: Time taken to admit to cleanup must not be overcounting if any of the steps other than the admit fails since it is run only once.
		Fix: Use the timestamp in Last Error if errorCode matches lastOperation and then determine if the metric was already updated.
		- Run and Cleanup: Since the Cleanup is a completely different flow, and in case of TTL etc the delete flow can run again. 
		FIX: Use the timestamp in Last Error if errorCode matches lastOperation and then determine if the metric was already updated.
*/

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
    // Number of unique tasks created (not overcounted by reconcile loops)
    MetricTasksCreated = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "etcd_operator",
            Subsystem: "task_controller",
            Name:      "tasks_created_total",
            Help:      "Total number of unique EtcdOperatorTasks created.",
        },
        []string{"namespace"},
    )

    // Number of tasks successfully run (incremented only at successful completion)
    MetricTasksSucceeded = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "etcd_operator",
            Subsystem: "task_controller",
            Name:      "tasks_succeeded_total",
            Help:      "Total number of EtcdOperatorTasks successfully run.",
        },
        []string{"namespace"},
    )

    // Time taken for each phase (admit, run, cleanup) in seconds
    MetricTaskPhaseDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "etcd_operator",
            Subsystem: "task_controller",
            Name:      "task_phase_duration_seconds",
            Help:      "Duration in seconds for each phase of EtcdOperatorTask (admit, run, cleanup).",
            Buckets:   prometheus.DefBuckets,
        },
        []string{"namespace", "task_name", "phase", "status"}, // status: success|failure
    )
)

func init() {
    metrics.Registry.MustRegister(MetricTasksCreated)
    metrics.Registry.MustRegister(MetricTasksSucceeded)
    metrics.Registry.MustRegister(MetricTaskPhaseDuration)
}