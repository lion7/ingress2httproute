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
	Scheme         *runtime.Scheme
	CrossNamespace bool
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/finalizers,verbs=update

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

	// Filter the rules so only those with a hostname are used
	rules := filterRules(ingress.Spec.Rules, logger)
	if len(rules) == 0 {
		logger.Info("no eligible ingress rules found for ingress", "ingress", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Extract hostnames from ingress rules
	hostnames := extractHostnames(rules)

	// Find matching gateways for the hostnames and collect unique parent refs
	parentRefs := r.findMatchingGateways(req.Namespace, hostnames, gateways)
	if len(parentRefs) == 0 {
		logger.Info("no eligible parentRefs found for ingress", "ingress", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	var defaultBackendRef *gatewayv1.HTTPBackendRef
	if ingress.Spec.DefaultBackend != nil {
		backendRef, err := r.mapBackendRef(ctx, req.Namespace, *ingress.Spec.DefaultBackend)
		if err != nil {
			return ctrl.Result{}, err
		}
		defaultBackendRef = &backendRef
	}

	// Create HTTPRoute rules for this ingress rule
	httpRouteRules, err := r.createHTTPRouteRules(ctx, req.Namespace, rules, defaultBackendRef)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(httpRouteRules) == 0 {
		logger.Info("no valid rules found for ingress", "ingress", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	spec := gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: parentRefs},
		Hostnames:       hostnames,
		Rules:           httpRouteRules,
	}

	// Create or update HTTPRoute for the ingress
	owner := createOwnerReference(ingress)
	if err := r.reconcileHTTPRoute(ctx, req.NamespacedName, owner, spec); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// findMatchingGateways finds all gateways that match the given hostnames
func (r *IngressReconciler) findMatchingGateways(ingressNamespace string, hostnames []gatewayv1.Hostname, gateways gatewayv1.GatewayList) []gatewayv1.ParentReference {
	// Map to track unique parent references
	parentRefMap := make(map[string]bool)
	var result []gatewayv1.ParentReference

	for _, hostname := range hostnames {
		for _, gateway := range gateways.Items {
			for _, listener := range gateway.Spec.Listeners {
				// Check namespace selector
				nsSelector := gatewayv1.NamespacesFromSame
				if listener.AllowedRoutes != nil && listener.AllowedRoutes.Namespaces != nil && listener.AllowedRoutes.Namespaces.From != nil {
					nsSelector = *listener.AllowedRoutes.Namespaces.From
				}
				nsMatches := nsSelector == gatewayv1.NamespacesFromAll || (nsSelector == gatewayv1.NamespacesFromSame && gateway.Namespace == ingressNamespace)
				if !nsMatches {
					continue
				}

				if listener.Hostname == nil {
					continue
				}

				if hostnameMatches(hostname, *listener.Hostname) {
					parentRef := createParentRef(gateway, listener)
					// Create a unique key for the parent reference
					key := fmt.Sprintf("%s/%s/%s/%s/%s",
						*parentRef.Group, *parentRef.Kind, *parentRef.Namespace, parentRef.Name, *parentRef.SectionName)
					if !parentRefMap[key] {
						result = append(result, parentRef)
						parentRefMap[key] = true
					}
				}
			}
		}
	}

	return result
}

// createHTTPRouteRules converts ingress HTTP rules to HTTPRoute rules
func (r *IngressReconciler) createHTTPRouteRules(ctx context.Context, namespace string, rules []networkingv1.IngressRule, defaultBackendRef *gatewayv1.HTTPBackendRef) ([]gatewayv1.HTTPRouteRule, error) {
	var result []gatewayv1.HTTPRouteRule

	for _, rule := range rules {
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				// Create a path match
				pathMatch := createPathMatch(path)
				matches := []gatewayv1.HTTPRouteMatch{{Path: &pathMatch}}

				// Create backend references
				backendRefs := make([]gatewayv1.HTTPBackendRef, 0)

				// Add the path-specific backend
				ref, err := r.mapBackendRef(ctx, namespace, path.Backend)
				if err != nil {
					return nil, err
				}
				backendRefs = append(backendRefs, ref)

				// Add the default backend ref if it exists and isn't yet included
				if defaultBackendRef != nil {
					if !containsBackendRef(backendRefs, *defaultBackendRef) {
						backendRefs = append(backendRefs, *defaultBackendRef)
					}
				}

				result = append(result, gatewayv1.HTTPRouteRule{
					Matches:     matches,
					BackendRefs: backendRefs,
				})
			}
		} else if defaultBackendRef != nil {
			result = append(result, gatewayv1.HTTPRouteRule{
				BackendRefs: []gatewayv1.HTTPBackendRef{*defaultBackendRef},
			})
		}
	}

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

func (r *IngressReconciler) mapBackendRef(ctx context.Context, namespace string, ref networkingv1.IngressBackend) (gatewayv1.HTTPBackendRef, error) {
	var objectRef gatewayv1.BackendObjectReference
	if ref.Resource != nil {
		if ref.Resource.APIGroup != nil {
			group := gatewayv1.Group(*ref.Resource.APIGroup)
			objectRef.Group = &group
		}
		kind := gatewayv1.Kind(ref.Resource.Kind)
		name := gatewayv1.ObjectName(ref.Resource.Name)
		objectRef.Kind = &kind
		objectRef.Name = name
		if !r.CrossNamespace {
			ns := gatewayv1.Namespace(namespace)
			objectRef.Namespace = &ns
		}
	} else if ref.Service != nil {
		group := gatewayv1.Group("")
		kind := gatewayv1.Kind("Service")
		name := gatewayv1.ObjectName(ref.Service.Name)
		objectRef.Group = &group
		objectRef.Kind = &kind
		objectRef.Name = name
		if !r.CrossNamespace {
			ns := gatewayv1.Namespace(namespace)
			objectRef.Namespace = &ns
		}
		if ref.Service.Port.Number != 0 {
			port := gatewayv1.PortNumber(ref.Service.Port.Number)
			objectRef.Port = &port
		} else if ref.Service.Port.Name != "" {
			svc := corev1.Service{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Service.Name}, &svc); err != nil {
				return gatewayv1.HTTPBackendRef{}, err
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
	one := int32(1)
	return gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{
		BackendObjectReference: objectRef,
		Weight:                 &one,
	}}, nil
}
