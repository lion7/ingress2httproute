package controller

import (
	"encoding/json"
	"strings"

	"github.com/go-logr/logr"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func filterRules(rules []networkingv1.IngressRule, logger logr.Logger) []networkingv1.IngressRule {
	var result []networkingv1.IngressRule

	for _, rule := range rules {
		if rule.Host == "" {
			logger.Info("ingress rule has no hostname", "rule", rule)
			continue
		}
		result = append(result, rule)
	}

	return result
}

func extractHostnames(rules []networkingv1.IngressRule) []gatewayv1.Hostname {
	// Map to track unique hostnames
	hostnameMap := make(map[string]bool)

	var hostnames []gatewayv1.Hostname
	for _, rule := range rules {
		if !hostnameMap[rule.Host] {
			hostnames = append(hostnames, gatewayv1.Hostname(rule.Host))
			hostnameMap[rule.Host] = true
		}
	}

	return hostnames
}

// hostnameMatches checks if the ingress hostname matches the gateway listener hostname
func hostnameMatches(ingressHostname, listenerHostname gatewayv1.Hostname) bool {
	ingressHost := string(ingressHostname)
	listenerHost := string(listenerHostname)

	if strings.HasPrefix(listenerHost, "*.") {
		fqdn := strings.TrimPrefix(listenerHost, "*.")
		return !strings.EqualFold(ingressHost, fqdn) && strings.HasSuffix(ingressHost, fqdn)
	}

	return strings.EqualFold(ingressHost, listenerHost)
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

// containsBackendRef checks if a backend reference already exists in the slice
func containsBackendRef(backendRefs []gatewayv1.HTTPBackendRef, ref gatewayv1.HTTPBackendRef) bool {
	for _, backendRef := range backendRefs {
		if isEqual(backendRef, ref) {
			return true
		}
	}
	return false
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
