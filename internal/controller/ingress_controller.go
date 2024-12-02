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
	"k8s.io/apimachinery/pkg/util/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	networkingv1 "k8s.io/api/networking/v1"
)

// IngressReconciler reconciles an Ingress object
type IngressReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

	httpRoute := gatewayv1.HTTPRoute{}
	httpRouteExists := true
	if err := r.Get(ctx, req.NamespacedName, &httpRoute); err != nil {
		if errors.IsNotFound(err) {
			httpRouteExists = false
		} else {
			return ctrl.Result{}, err
		}
	}

	ingress := networkingv1.Ingress{}
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		logger.Error(err, fmt.Sprintf("cannot reconcile ingress %s", req.NamespacedName))
		if errors.IsNotFound(err) {
			if httpRouteExists {
				if err := r.Delete(ctx, &httpRoute); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// map the hostnames to Gateway API format
	var hostnames []gatewayv1.Hostname
	for _, rule := range ingress.Spec.Rules {
		if rule.Host != "" {
			if len(hostnames) == 0 {
				hostnames = append(hostnames, gatewayv1.Hostname(rule.Host))
			} else if expected := string(hostnames[0]); rule.Host != expected {
				return ctrl.Result{}, fmt.Errorf("hostname mismatch, expected %s but got %s", expected, rule.Host)
			}
		}
	}
	if hostnames == nil {
		logger.Info("no hostnames specified")
		return ctrl.Result{}, nil
	}

	// map the rules to Gateway API format
	var rules []gatewayv1.HTTPRouteRule
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP != nil {
			var matches []gatewayv1.HTTPRouteMatch
			var backendRefs []gatewayv1.HTTPBackendRef
			for _, path := range rule.HTTP.Paths {
				pathMatch := gatewayv1.HTTPPathMatch{}
				pathMatch.Value = &path.Path
				if path.PathType == nil {
					pathMatch.Type = nil
				} else if *path.PathType == networkingv1.PathTypeExact {
					exact := gatewayv1.PathMatchExact
					pathMatch.Type = &exact
				} else if *path.PathType == networkingv1.PathTypePrefix {
					prefix := gatewayv1.PathMatchPathPrefix
					pathMatch.Type = &prefix
				}
				matches = append(matches, gatewayv1.HTTPRouteMatch{Path: &pathMatch})

				ref, err := mapBackendRef(ctx, r.Client, req.Namespace, path.Backend)
				if err != nil {
					return ctrl.Result{}, err
				}
				duplicate := false
				for _, backendRef := range backendRefs {
					if isEqual(backendRef, ref) {
						duplicate = true
						break
					}
				}
				if !duplicate {
					backendRefs = append(backendRefs, ref)
				}
			}
			if len(backendRefs) > 0 && ingress.Spec.DefaultBackend != nil {
				ref, err := mapBackendRef(ctx, r.Client, req.Namespace, *ingress.Spec.DefaultBackend)
				if err != nil {
					return ctrl.Result{}, err
				}
				duplicate := false
				for _, backendRef := range backendRefs {
					if isEqual(backendRef, ref) {
						duplicate = true
						break
					}
				}
				if !duplicate {
					backendRefs = append(backendRefs, ref)
				}
			}
			rules = append(rules, gatewayv1.HTTPRouteRule{
				Matches:     matches,
				BackendRefs: backendRefs,
			})
		}
	}
	if rules == nil {
		logger.Info("no rules specified")
		return ctrl.Result{}, nil
	}

	// Find matching gateways
	var parentRefs []gatewayv1.ParentReference
	var gateways gatewayv1.GatewayList
	if err := r.List(ctx, &gateways); err != nil {
		logger.Error(err, "cannot list gateways")
		return ctrl.Result{}, err
	}
	for _, gateway := range gateways.Items {
	listeners:
		for _, listener := range gateway.Spec.Listeners {
			nsSelector := gatewayv1.NamespacesFromSame
			if listener.AllowedRoutes != nil && listener.AllowedRoutes.Namespaces != nil && listener.AllowedRoutes.Namespaces.From != nil {
				nsSelector = *listener.AllowedRoutes.Namespaces.From
			}
			nsMatches := nsSelector == gatewayv1.NamespacesFromAll || (nsSelector == gatewayv1.NamespacesFromSame && gateway.Namespace == req.Namespace)
			if !nsMatches {
				continue
			}
			if listener.Hostname == nil {
				continue
			}
			hostname := string(*listener.Hostname)
			if strings.HasPrefix(hostname, "*.") {
				fqdn := strings.TrimPrefix(hostname, "*.")
				for _, s := range hostnames {
					if !strings.EqualFold(string(s), fqdn) && strings.HasSuffix(string(s), fqdn) {
						parentRefs = append(parentRefs, createParentRef(gateway, listener.Name))
						continue listeners
					}
				}
			} else {
				for _, s := range hostnames {
					if strings.EqualFold(string(s), hostname) {
						parentRefs = append(parentRefs, createParentRef(gateway, listener.Name))
						continue listeners
					}
				}
			}
		}
	}
	if parentRefs == nil {
		logger.Info("could not find matching gateways")
		return ctrl.Result{}, nil
	}

	newSpec := gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: parentRefs},
		Hostnames:       hostnames,
		Rules:           rules,
	}

	if !httpRouteExists {
		// Create the owner reference
		bTrue := true
		owner := metav1.OwnerReference{
			APIVersion:         ingress.APIVersion,
			Kind:               ingress.Kind,
			Name:               ingress.Name,
			UID:                ingress.UID,
			Controller:         &bTrue,
			BlockOwnerDeletion: &bTrue,
		}

		httpRoute.SetNamespace(req.Namespace)
		httpRoute.SetName(req.Name)
		httpRoute.SetOwnerReferences([]metav1.OwnerReference{owner})
		if err := r.Create(ctx, &httpRoute); err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("created equivalent HTTPRoute for ingress")
	} else if !isEqual(httpRoute.Spec, newSpec) {
		httpRoute.Spec = newSpec
		if err := r.Update(ctx, &httpRoute); err != nil {
			return ctrl.Result{}, err
		}
		logger.Info("updated equivalent HTTPRoute for ingress")
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Complete(r)
}

func createParentRef(gateway gatewayv1.Gateway, section gatewayv1.SectionName) gatewayv1.ParentReference {
	gvk := gateway.GroupVersionKind()
	group := gatewayv1.Group(gvk.Group)
	kind := gatewayv1.Kind(gvk.Kind)
	namespace := gatewayv1.Namespace(gateway.Namespace)
	name := gatewayv1.ObjectName(gateway.Name)
	return gatewayv1.ParentReference{
		Group:       &group,
		Kind:        &kind,
		Namespace:   &namespace,
		Name:        name,
		SectionName: &section,
	}
}

func mapBackendRef(ctx context.Context, c client.Client, namespace string, ref networkingv1.IngressBackend) (gatewayv1.HTTPBackendRef, error) {
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
	} else if ref.Service != nil {
		group := gatewayv1.Group("")
		kind := gatewayv1.Kind("Service")
		name := gatewayv1.ObjectName(ref.Service.Name)
		objectRef.Group = &group
		objectRef.Kind = &kind
		objectRef.Name = name
		if ref.Service.Port.Number != 0 {
			port := gatewayv1.PortNumber(ref.Service.Port.Number)
			objectRef.Port = &port
		} else if ref.Service.Port.Name != "" {
			svc := corev1.Service{}
			if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Service.Name}, &svc); err != nil {
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

func isEqual(a, b interface{}) bool {
	if l, err := json.Marshal(a); err != nil {
		return false
	} else if r, err := json.Marshal(b); err != nil {
		return false
	} else {
		return string(l) == string(r)
	}
}
