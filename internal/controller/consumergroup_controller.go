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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/flohansen/konsumer-operator/api/v1alpha1"
	"github.com/flohansen/konsumer-operator/internal/monitor"
)

// ConsumerGroupReconciler reconciles a ConsumerGroup object
type ConsumerGroupReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Monitors map[string]*monitor.LagMonitor
}

//+kubebuilder:rbac:groups=github.com,resources=consumergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=github.com,resources=consumergroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=github.com,resources=consumergroups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ConsumerGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	r.stopMonitorSafe(req.String())

	var group v1alpha1.ConsumerGroup
	if err := r.Get(ctx, req.NamespacedName, &group); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	lagMonitor := &monitor.LagMonitor{}
	r.Monitors[req.String()] = lagMonitor
	go lagMonitor.Start(context.Background(), group.Spec.GroupID, group.Spec.Topic)

	return ctrl.Result{}, nil
}

func (r *ConsumerGroupReconciler) stopMonitorSafe(key string) {
	if monitor, ok := r.Monitors[key]; ok {
		monitor.Stop()
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConsumerGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ConsumerGroup{}).
		Complete(r)
}
