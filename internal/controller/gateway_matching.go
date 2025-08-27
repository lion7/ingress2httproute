package controller

import (
	"fmt"
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// findMatchingGateways finds all gateways that match the given hostname
func findMatchingGateways(ingressNamespace string, hostname gatewayv1.Hostname, gateways gatewayv1.GatewayList) []gatewayv1.ParentReference {
	// Map to track unique parent references
	parentRefMap := make(map[string]bool)
	var result []gatewayv1.ParentReference

	for _, gateway := range gateways.Items {
		for _, listener := range gateway.Spec.Listeners {
			if !isListenerAccessibleFromNamespace(listener, gateway.Namespace, ingressNamespace) {
				continue
			}

			// If the listener has no hostname, it's a catch-all that matches any hostname
			var matches bool
			if listener.Hostname == nil {
				matches = true
			} else {
				matches = hostnameMatches(hostname, *listener.Hostname)
			}

			if matches {
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

	return result
}

// findCatchAllGateways finds gateways with catch-all listeners (no hostname or wildcard)
func findCatchAllGateways(ingressNamespace string, gateways gatewayv1.GatewayList) []gatewayv1.ParentReference {
	var parentRefs []gatewayv1.ParentReference
	for _, gateway := range gateways.Items {
		for _, listener := range gateway.Spec.Listeners {
			if !isListenerAccessibleFromNamespace(listener, gateway.Namespace, ingressNamespace) {
				continue
			}
			// Accept gateways with no hostname (catch-all) or wildcard
			if listener.Hostname == nil || strings.HasPrefix(string(*listener.Hostname), "*") {
				parentRef := createParentRef(gateway, listener)
				parentRefs = append(parentRefs, parentRef)
			}
		}
	}
	return parentRefs
}

// isListenerAccessibleFromNamespace checks if a listener allows routes from the given namespace
func isListenerAccessibleFromNamespace(listener gatewayv1.Listener, gatewayNamespace, ingressNamespace string) bool {
	nsSelector := gatewayv1.NamespacesFromSame
	if listener.AllowedRoutes != nil && listener.AllowedRoutes.Namespaces != nil && listener.AllowedRoutes.Namespaces.From != nil {
		nsSelector = *listener.AllowedRoutes.Namespaces.From
	}
	return nsSelector == gatewayv1.NamespacesFromAll || (nsSelector == gatewayv1.NamespacesFromSame && gatewayNamespace == ingressNamespace)
}
