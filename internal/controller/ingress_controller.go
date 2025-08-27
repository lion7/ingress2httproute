/*
Copyright 2024 Gerard de Leeuw.

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
	"slices"

	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// IngressReconciler reconciles an Ingress object
type IngressReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	RequireHostname bool
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/finalizers,verbs=update
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ingress := networkingv1.Ingress{}
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, fmt.Sprintf("cannot reconcile Ingress %s", req.NamespacedName))
		return ctrl.Result{}, err
	}

	var gateways gatewayv1.GatewayList
	if err := r.List(ctx, &gateways); err != nil {
		logger.Error(err, "cannot list gateways")
		return ctrl.Result{}, err
	}

	if len(gateways.Items) == 0 {
		logger.Info("no gateways found")
		return ctrl.Result{}, nil
	}

	if len(ingress.Spec.Rules) == 0 {
		logger.Info("no rules found")
		return ctrl.Result{}, nil
	}

	// Create owner reference early for reuse
	owner := createOwnerReference(ingress)

	// Group rules by hostname
	ingressRules := groupRulesByHostname(ingress.Spec.Rules)

	// Map gateways to parent refs, grouped by hostname
	parentRefs := groupGatewaysByHostNameAndMapToParentRefs(ingress.Namespace, gateways)

	// Create one HTTPRoute per hostname as per mapping specification
	for hostname, matchingRules := range ingressRules {
		// Generate HTTPRoute name based on ingress name and hostname
		routeName := types.NamespacedName{
			Name:      generateHTTPRouteName(req.Name, hostname),
			Namespace: req.Namespace,
		}

		// Find parent refs matching this hostname
		routeParentRefs := findMatchingGateways(hostname, parentRefs)
		if len(routeParentRefs) == 0 {
			continue
		}

		// Create HTTPRoute hostnames slice
		var routeHostnames []gatewayv1.Hostname
		if hostname != "" {
			routeHostnames = append(routeHostnames, gatewayv1.Hostname(hostname))
		}

		// Map the Ingress rules for this specific hostname to HTTPRoute rules
		routeRules, err := r.mapToHTTPRouteRules(ctx, req.Namespace, matchingRules)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(routeRules) == 0 {
			continue
		}

		// Create the HTTPRoute spec
		spec := gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: routeParentRefs},
			Hostnames:       routeHostnames,
			Rules:           routeRules,
		}

		// Create or update HTTPRoute for this hostname
		if err := r.reconcileHTTPRoute(ctx, routeName, owner, spec); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// mapToHTTPRouteRules converts ingress HTTP rules to HTTPRoute rules
func (r *IngressReconciler) mapToHTTPRouteRules(ctx context.Context, namespace string, rules []networkingv1.IngressRule) ([]gatewayv1.HTTPRouteRule, error) {
	var result []gatewayv1.HTTPRouteRule

	for _, rule := range rules {
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				// Create a path match
				pathMatch := createPathMatch(path)

				// Create a backend reference
				backendRef, err := r.mapBackendRef(ctx, namespace, path.Backend)
				if err != nil {
					return nil, err
				}
				if backendRef == nil {
					return nil, fmt.Errorf("no backend found for path '%s'", path.Path)
				}

				result = append(result, gatewayv1.HTTPRouteRule{
					Matches:     []gatewayv1.HTTPRouteMatch{{Path: &pathMatch}},
					BackendRefs: []gatewayv1.HTTPBackendRef{*backendRef},
				})
			}
		}
	}

	slices.SortStableFunc(result, compareHTTPRouteRule)
	return result, nil
}

// reconcileHTTPRoute creates or updates a single HTTPRoute for the ingress
func (r *IngressReconciler) reconcileHTTPRoute(ctx context.Context, name types.NamespacedName, owner metav1.OwnerReference, spec gatewayv1.HTTPRouteSpec) error {
	logger := log.FromContext(ctx)
	httpRoute := gatewayv1.HTTPRoute{}
	httpRouteExists := true
	if err := r.Get(ctx, name, &httpRoute); err != nil {
		if errors.IsNotFound(err) {
			httpRouteExists = false
		} else {
			return err
		}
	}

	if !httpRouteExists {
		// Create a new HTTPRoute
		httpRoute.SetNamespace(name.Namespace)
		httpRoute.SetName(name.Name)
		httpRoute.SetOwnerReferences([]metav1.OwnerReference{owner})
		httpRoute.Spec = spec

		if err := r.Create(ctx, &httpRoute); err != nil {
			return err
		}

		logger.Info("created HTTPRoute", "name", name)
	} else if isOwnedBy(httpRoute.ObjectMeta, owner) && !isEqual(httpRoute.Spec, spec) {
		// Update existing HTTPRoute
		httpRoute.Spec = spec
		if err := r.Update(ctx, &httpRoute); err != nil {
			return err
		}
		logger.Info("updated HTTPRoute", "name", name)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Watches(
			&gatewayv1.Gateway{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				// This will trigger reconciliation for all Ingresses when any Gateway changes
				var requests []reconcile.Request

				ingressList := &networkingv1.IngressList{}
				if err := r.List(ctx, ingressList); err != nil {
					return requests
				}

				for _, ingress := range ingressList.Items {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      ingress.Name,
							Namespace: ingress.Namespace,
						},
					})
				}

				return requests
			}),
		).
		Complete(r)
}

func (r *IngressReconciler) mapBackendRef(ctx context.Context, namespace string, ref networkingv1.IngressBackend) (*gatewayv1.HTTPBackendRef, error) {
	var objectRef gatewayv1.BackendObjectReference
	if ref.Resource != nil {
		if ref.Resource.APIGroup != nil {
			group := gatewayv1.Group(*ref.Resource.APIGroup)
			objectRef.Group = &group
		}
		kind := gatewayv1.Kind(ref.Resource.Kind)
		ns := gatewayv1.Namespace(namespace)
		name := gatewayv1.ObjectName(ref.Resource.Name)
		objectRef.Kind = &kind
		objectRef.Namespace = &ns
		objectRef.Name = name
	} else if ref.Service != nil {
		group := gatewayv1.Group("")
		kind := gatewayv1.Kind("Service")
		ns := gatewayv1.Namespace(namespace)
		name := gatewayv1.ObjectName(ref.Service.Name)
		objectRef.Group = &group
		objectRef.Kind = &kind
		objectRef.Namespace = &ns
		objectRef.Name = name
		if ref.Service.Port.Number != 0 {
			port := gatewayv1.PortNumber(ref.Service.Port.Number)
			objectRef.Port = &port
		} else if ref.Service.Port.Name != "" {
			svc := corev1.Service{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Service.Name}, &svc); err != nil {
				return nil, err
			}
			for _, svcPort := range svc.Spec.Ports {
				if svcPort.Name == ref.Service.Port.Name {
					port := gatewayv1.PortNumber(svcPort.Port)
					objectRef.Port = &port
					break
				}
			}
		}
	}
	backendRef := gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{
		BackendObjectReference: objectRef,
	}}
	return &backendRef, nil
}
