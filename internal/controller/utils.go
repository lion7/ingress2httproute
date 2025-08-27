package controller

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// generateHTTPRouteName creates a HTTPRoute name from ingress name and hostname
// Following the pattern: ingressName-hostname-with-dots-replaced-by-dashes
func generateHTTPRouteName(ingressName, hostname string) string {
	if hostname == "" {
		return ingressName
	} else {
		// Replace dots and special characters with dashes for valid Kubernetes names
		cleanHostname := strings.ReplaceAll(hostname, ".", "-")
		cleanHostname = strings.ReplaceAll(cleanHostname, "*", "wildcard")
		return fmt.Sprintf("%s-%s", ingressName, cleanHostname)
	}
}

func createOwnerReference(ingress networkingv1.Ingress) metav1.OwnerReference {
	bTrue := true
	return metav1.OwnerReference{
		APIVersion:         "networking.k8s.io/v1",
		Kind:               "Ingress",
		Name:               ingress.Name,
		UID:                ingress.UID,
		Controller:         &bTrue,
		BlockOwnerDeletion: &bTrue,
	}
}

func createParentRef(gateway gatewayv1.Gateway, listener gatewayv1.Listener) gatewayv1.ParentReference {
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
		SectionName: &listener.Name,
	}
}

// createPathMatch creates a gateway API path match from an ingress path
func createPathMatch(path networkingv1.HTTPIngressPath) gatewayv1.HTTPPathMatch {
	pathMatch := gatewayv1.HTTPPathMatch{Value: &path.Path}

	if path.PathType == nil {
		pathMatch.Type = nil
	} else {
		switch *path.PathType {
		case networkingv1.PathTypeExact:
			exact := gatewayv1.PathMatchExact
			pathMatch.Type = &exact
		case networkingv1.PathTypePrefix:
			prefix := gatewayv1.PathMatchPathPrefix
			pathMatch.Type = &prefix
		case networkingv1.PathTypeImplementationSpecific:
			regex := gatewayv1.PathMatchRegularExpression
			pathMatch.Type = &regex
		}
	}

	return pathMatch
}

// groupRulesByHostname groups ingress rules by hostname
func groupRulesByHostname(rules []networkingv1.IngressRule) map[string][]networkingv1.IngressRule {
	result := make(map[string][]networkingv1.IngressRule)

	for _, rule := range rules {
		hostname := rule.Host
		existingRules := result[hostname]
		result[hostname] = append(existingRules, rule)
	}

	return result
}

// groupGatewaysByHostNameAndMapToParentRefs groups gateways by hostname and maps each listener to a parent ref
func groupGatewaysByHostNameAndMapToParentRefs(ingressNamespace string, gateways gatewayv1.GatewayList) map[string][]gatewayv1.ParentReference {
	result := make(map[string][]gatewayv1.ParentReference)

	for _, gateway := range gateways.Items {
		for _, listener := range gateway.Spec.Listeners {
			if !isListenerAccessibleFromNamespace(listener, gateway.Namespace, ingressNamespace) {
				continue
			}

			// If the listener has no hostname, it's a catch-all that matches any hostname
			var hostname string
			if listener.Hostname == nil {
				hostname = ""
			} else {
				hostname = string(*listener.Hostname)
			}

			existingParentRefs := result[hostname]
			result[hostname] = append(existingParentRefs, createParentRef(gateway, listener))
		}
	}

	return result
}

// isListenerAccessibleFromNamespace checks if a listener allows routes from the given namespace
func isListenerAccessibleFromNamespace(listener gatewayv1.Listener, gatewayNamespace, ingressNamespace string) bool {
	nsSelector := gatewayv1.NamespacesFromSame
	if listener.AllowedRoutes != nil && listener.AllowedRoutes.Namespaces != nil && listener.AllowedRoutes.Namespaces.From != nil {
		nsSelector = *listener.AllowedRoutes.Namespaces.From
	}
	return nsSelector == gatewayv1.NamespacesFromAll || (nsSelector == gatewayv1.NamespacesFromSame && gatewayNamespace == ingressNamespace)
}

// findMatchingParentRefs finds all parentRefs that match the given hostname
func findMatchingGateways(ingressHost string, parentRefsGroupedByHostname map[string][]gatewayv1.ParentReference) []gatewayv1.ParentReference {
	var result []gatewayv1.ParentReference

	for hostname, references := range parentRefsGroupedByHostname {
		if hostname == "" || hostnameMatches(ingressHost, hostname) {
			result = append(result, references...)
		}
	}

	slices.SortStableFunc(result, compareParentRef)
	return result
}

// hostnameMatches checks if the ingress hostname matches the gateway listener hostname
func hostnameMatches(ingressHost, listenerHost string) bool {
	if strings.HasPrefix(listenerHost, "*.") {
		fqdn := strings.TrimPrefix(listenerHost, "*.")
		// Allow only subdomain matches and not exact domain matches for wildcard patterns
		return strings.HasSuffix(ingressHost, fqdn) && !strings.EqualFold(ingressHost, fqdn)
	}

	return strings.EqualFold(ingressHost, listenerHost)
}

func isOwnedBy(metadata metav1.ObjectMeta, owner metav1.OwnerReference) bool {
	for _, reference := range metadata.OwnerReferences {
		if reference.APIVersion == owner.APIVersion && reference.Kind == owner.Kind && reference.Name == owner.Name {
			return true
		}
	}
	return false
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
