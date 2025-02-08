/*
Copyright 2025.

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

package controller

import (
	"context"
	"fmt"
	"math"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/flohansen/konsumer-operator/api/v1alpha1"
)

const (
	deploymentOwnerKey = ".metadata.controller"
)

// ConsumerGroupReconciler reconciles a ConsumerGroup object
type ConsumerGroupReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Consumer *kafka.Consumer
}

//+kubebuilder:rbac:groups=github.com,resources=consumergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=github.com,resources=consumergroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=github.com,resources=consumergroups/finalizers,verbs=update

func (r *ConsumerGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var group v1alpha1.ConsumerGroup
	if err := r.Get(ctx, req.NamespacedName, &group); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	refreshInterval, err := time.ParseDuration(group.Spec.RefreshInterval)
	if err != nil {
		return ctrl.Result{}, err
	}

	var deployments appsv1.DeploymentList
	if err := r.List(ctx, &deployments, client.InNamespace(req.Namespace), client.MatchingFields{deploymentOwnerKey: req.Name}); err != nil {
		return ctrl.Result{}, err
	}

	if len(deployments.Items) == 0 {
		if err := r.createConsumerDeployment(ctx, &group); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: refreshInterval}, nil
	}

	consumerLag, err := r.calculateConsumerLag(ctx, &group)
	if err != nil {
		return ctrl.Result{}, err
	}

	replicas := r.calculateDeploymentReplicas(consumerLag, &group)
	if err := r.scaleDeployment(ctx, &deployments.Items[0], replicas); err != nil {
		log.Error(err, "unable to scale deployment")
		return ctrl.Result{RequeueAfter: refreshInterval}, nil
	}

	return ctrl.Result{RequeueAfter: refreshInterval}, nil
}

func (r *ConsumerGroupReconciler) scaleDeployment(ctx context.Context, deployment *appsv1.Deployment, replicas int32) error {
	if *deployment.Spec.Replicas == replicas {
		return nil
	}

	deployment.Spec.Replicas = ptr.To(replicas)
	return r.Update(ctx, deployment)
}

func (r *ConsumerGroupReconciler) calculateConsumerLag(ctx context.Context, group *v1alpha1.ConsumerGroup) (int64, error) {
	client, err := kafka.NewAdminClientFromConsumer(r.Consumer)
	if err != nil {
		return -1, err
	}

	dtr, err := client.DescribeTopics(ctx, kafka.NewTopicCollectionOfTopicNames([]string{group.Spec.Topic}))
	if err != nil {
		return -1, err
	}

	var partitions []kafka.TopicPartition
	for _, topic := range dtr.TopicDescriptions {
		for _, partition := range topic.Partitions {
			partitions = append(partitions, kafka.TopicPartition{
				Topic:     &group.Spec.Topic,
				Partition: int32(partition.Partition),
			})
		}
	}

	lcgo, err := client.ListConsumerGroupOffsets(ctx, []kafka.ConsumerGroupTopicPartitions{{
		Group:      group.Spec.GroupID,
		Partitions: partitions,
	}})
	if err != nil {
		return -1, err
	}

	consumerLags := make(map[string][]int64)
	for _, topic := range lcgo.ConsumerGroupsTopicPartitions {
		for _, partition := range topic.Partitions {
			_, high, err := r.Consumer.QueryWatermarkOffsets(*partition.Topic, partition.Partition, 500)
			if err != nil {
				return -1, err
			}

			currentOffset := int64(partition.Offset)
			lag := high - currentOffset
			consumerLags[*partition.Topic] = append(consumerLags[*partition.Topic], lag)
		}
	}

	avgs := make(map[string]int64)
	for topic, partitions := range consumerLags {
		sum := int64(0)
		for _, partitionLag := range partitions {
			sum += partitionLag
		}

		avgs[topic] = sum
	}

	return avgs[group.Spec.Topic], nil
}

func (r *ConsumerGroupReconciler) calculateDeploymentReplicas(lag int64, group *v1alpha1.ConsumerGroup) int32 {
	p := math.Min(float64(lag)/float64(group.Spec.OffsetThreshold), 1.0)
	x := float64(group.Spec.MinReplicas) + p*float64(group.Spec.MaxReplicas-group.Spec.MinReplicas)
	return int32(x)
}

func (r *ConsumerGroupReconciler) createConsumerDeployment(ctx context.Context, group *v1alpha1.ConsumerGroup) error {
	name := fmt.Sprintf("%s-consumer", group.ObjectMeta.Name)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-deployment-%d", name, metav1.Now().Unix()),
			Namespace: group.ObjectMeta.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: group.Spec.Containers,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(group, deployment, r.Scheme); err != nil {
		return err
	}

	if err := r.Create(ctx, deployment); err != nil {
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConsumerGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &appsv1.Deployment{}, deploymentOwnerKey, func(obj client.Object) []string {
		deployment := obj.(*appsv1.Deployment)
		owner := metav1.GetControllerOf(deployment)
		if owner == nil {
			return nil
		}

		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ConsumerGroup{}).
		Complete(r)
}
