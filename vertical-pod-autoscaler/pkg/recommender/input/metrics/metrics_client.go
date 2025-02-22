/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	k8sapiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/autoscaler/vertical-pod-autoscaler/pkg/recommender/model"
	"k8s.io/autoscaler/vertical-pod-autoscaler/pkg/utils/metrics"
	klog "k8s.io/klog/v2"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
	resourceclient "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"
)

const (
	metricsNamespace = metrics.TopMetricsNamespace + "metrics-server-client"
)

var metricServerResponses = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: metricsNamespace,
		Name:      "metric_server_responses",
		Help:      "Count of responses to queries to metrics server",
	}, []string{"is_error"},
)

// Register initializes all VPA metrics for metrics server client
func Register() {
	prometheus.MustRegister(metricServerResponses)
}

// ContainerMetricsSnapshot contains information about usage of certain container within defined time window.
type ContainerMetricsSnapshot struct {
	// ID identifies a specific container those metrics are coming from.
	ID model.ContainerID
	// End time of the measurement interval.
	SnapshotTime time.Time
	// Duration of the measurement interval, which is [SnapshotTime - SnapshotWindow, SnapshotTime].
	SnapshotWindow time.Duration
	// Actual usage of the resources over the measurement interval.
	Usage model.Resources
}

// MetricsClient provides simple metrics on resources usage on containter level.
type MetricsClient interface {
	// GetContainersMetrics returns an array of ContainerMetricsSnapshots,
	// representing resource usage for every running container in the cluster
	GetContainersMetrics() ([]*ContainerMetricsSnapshot, error)
}

type metricsClient struct {
	metricsGetter resourceclient.PodMetricsesGetter
	namespace     string
}

// NewMetricsClient creates new instance of MetricsClient, which is used by recommender.
// It requires an instance of PodMetricsesGetter, which is used for underlying communication with metrics server.
// namespace limits queries to particular namespace, use k8sapiv1.NamespaceAll to select all namespaces.
func NewMetricsClient(metricsGetter resourceclient.PodMetricsesGetter, namespace string) MetricsClient {
	return &metricsClient{
		metricsGetter: metricsGetter,
		namespace:     namespace,
	}
}

func (c *metricsClient) GetContainersMetrics() ([]*ContainerMetricsSnapshot, error) {
	var metricsSnapshots []*ContainerMetricsSnapshot

	podMetricsInterface := c.metricsGetter.PodMetricses(c.namespace)
	podMetricsList, err := podMetricsInterface.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		metricServerResponses.WithLabelValues(strconv.FormatBool(true)).Inc()
		return nil, err
	}
	klog.V(3).Infof("%v podMetrics retrieved for all namespaces", len(podMetricsList.Items))
	for _, podMetrics := range podMetricsList.Items {
		metricsSnapshotsForPod := createContainerMetricsSnapshots(podMetrics)
		metricsSnapshots = append(metricsSnapshots, metricsSnapshotsForPod...)
	}
	metricServerResponses.WithLabelValues(strconv.FormatBool(false)).Inc()
	return metricsSnapshots, nil
}

func createContainerMetricsSnapshots(podMetrics v1beta1.PodMetrics) []*ContainerMetricsSnapshot {
	snapshots := make([]*ContainerMetricsSnapshot, len(podMetrics.Containers))
	for i, containerMetrics := range podMetrics.Containers {
		snapshots[i] = newContainerMetricsSnapshot(containerMetrics, podMetrics)
	}
	return snapshots
}

func newContainerMetricsSnapshot(containerMetrics v1beta1.ContainerMetrics, podMetrics v1beta1.PodMetrics) *ContainerMetricsSnapshot {
	usage := calculateUsage(containerMetrics.Usage)

	return &ContainerMetricsSnapshot{
		ID: model.ContainerID{
			ContainerName: containerMetrics.Name,
			PodID: model.PodID{
				Namespace: podMetrics.Namespace,
				PodName:   podMetrics.Name,
			},
		},
		Usage:          usage,
		SnapshotTime:   podMetrics.Timestamp.Time,
		SnapshotWindow: podMetrics.Window.Duration,
	}
}

func calculateUsage(containerUsage k8sapiv1.ResourceList) model.Resources {
	cpuQuantity := containerUsage[k8sapiv1.ResourceCPU]
	cpuMillicores := cpuQuantity.MilliValue()

	memoryQuantity := containerUsage[k8sapiv1.ResourceMemory]
	memoryBytes := memoryQuantity.Value()

	return model.Resources{
		model.ResourceCPU:    model.ResourceAmount(cpuMillicores),
		model.ResourceMemory: model.ResourceAmount(memoryBytes),
	}
}
