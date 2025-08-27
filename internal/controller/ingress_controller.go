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
	"strings"

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

	// Create owner reference early for reuse
	owner := createOwnerReference(ingress)

	// Map default backend ref if it exists (reused across all routes)
	var defaultBackendRef *gatewayv1.HTTPBackendRef
	if ingress.Spec.DefaultBackend != nil {
		backendRef, err := r.mapBackendRef(ctx, req.Namespace, *ingress.Spec.DefaultBackend)
		if err != nil {
			return ctrl.Result{}, err
		}
		defaultBackendRef = &backendRef
	}

	// Extract hostnames from ingress rules
	hostnames := extractHostnames(ingress.Spec.Rules)

	// If there are no rules with hostnames, we do special handling
	if len(hostnames) == 0 {
		// if we don't require a hostname and there's a default backend, create a catch-all HTTPRoute
		if !r.RequireHostname && defaultBackendRef != nil {
			logger.Info("creating catch-all HTTPRoute for default backend only", "ingress", req.NamespacedName)
			spec, err := createDefaultBackendSpec(req.Namespace, *defaultBackendRef, gateways)
			if err != nil {
				return ctrl.Result{}, err
			}
			if err := r.reconcileHTTPRoute(ctx, req.NamespacedName, owner, spec); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			logger.Info("no eligible ingress rules found for ingress", "ingress", req.NamespacedName)
		}
		return ctrl.Result{}, nil
	}

	// Create one HTTPRoute per hostname as per mapping specification
	for _, hostname := range hostnames {
		// Find rules that match this hostname
		var matchingRules []networkingv1.IngressRule
		for _, rule := range ingress.Spec.Rules {
			if rule.Host == string(hostname) {
				matchingRules = append(matchingRules, rule)
			}
		}

		// Create HTTPRoute rules for this specific hostname
		rules, err := r.createHTTPRouteRules(ctx, req.Namespace, matchingRules, defaultBackendRef)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(rules) == 0 {
			continue
		}

		// Find parent refs specific to this hostname
		parentRefs := findMatchingGateways(req.Namespace, hostname, gateways)
		if len(parentRefs) == 0 {
			continue
		}

		spec := gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: parentRefs},
			Hostnames:       []gatewayv1.Hostname{hostname},
			Rules:           rules,
		}

		// Generate HTTPRoute name based on ingress name + hostname
		httpRouteName := generateHTTPRouteName(req.Name, string(hostname))
		httpRouteNamespacedName := types.NamespacedName{
			Name:      httpRouteName,
			Namespace: req.Namespace,
		}

		// Create or update HTTPRoute for this hostname
		if err := r.reconcileHTTPRoute(ctx, httpRouteNamespacedName, owner, spec); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// createDefaultBackendSpec creates a spec for catch-all HTTPRoute for default backend only ingresses
func createDefaultBackendSpec(ingressNamespace string, backendRef gatewayv1.HTTPBackendRef, gateways gatewayv1.GatewayList) (gatewayv1.HTTPRouteSpec, error) {
	// Find gateways with catch-all listeners (no hostname or wildcard)
	parentRefs := findCatchAllGateways(ingressNamespace, gateways)
	if len(parentRefs) == 0 {
		return gatewayv1.HTTPRouteSpec{}, fmt.Errorf("no catch-all gateways found for default backend")
	}

	// Create a catch-all HTTPRoute
	return gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: parentRefs},
		// No hostnames means this is a catch-all route
		Rules: []gatewayv1.HTTPRouteRule{{
			BackendRefs: []gatewayv1.HTTPBackendRef{backendRef},
		}},
	}, nil
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
			// For rules without an HTTP section, create a default path match
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

// generateHTTPRouteName creates a HTTPRoute name from ingress name and hostname
// Following the pattern: ingressName-hostname-with-dots-replaced-by-dashes
func generateHTTPRouteName(ingressName, hostname string) string {
	// Replace dots and special characters with dashes for valid Kubernetes names
	cleanHostname := strings.ReplaceAll(hostname, ".", "-")
	cleanHostname = strings.ReplaceAll(cleanHostname, "*", "wildcard")
	return fmt.Sprintf("%s-%s", ingressName, cleanHostname)
}
