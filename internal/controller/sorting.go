package controller

import (
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func compareParentRef(a, b gatewayv1.ParentReference) int {
	// Compare by group first
	aGroup := ""
	if a.Group != nil {
		aGroup = string(*a.Group)
	}
	bGroup := ""
	if b.Group != nil {
		bGroup = string(*b.Group)
	}
	if cmp := strings.Compare(aGroup, bGroup); cmp != 0 {
		return cmp
	}

	// Compare by kind
	aKind := ""
	if a.Kind != nil {
		aKind = string(*a.Kind)
	}
	bKind := ""
	if b.Kind != nil {
		bKind = string(*b.Kind)
	}
	if cmp := strings.Compare(aKind, bKind); cmp != 0 {
		return cmp
	}

	// Compare by namespace
	aNamespace := ""
	if a.Namespace != nil {
		aNamespace = string(*a.Namespace)
	}
	bNamespace := ""
	if b.Namespace != nil {
		bNamespace = string(*b.Namespace)
	}
	if cmp := strings.Compare(aNamespace, bNamespace); cmp != 0 {
		return cmp
	}

	// Compare by name
	if cmp := strings.Compare(string(a.Name), string(b.Name)); cmp != 0 {
		return cmp
	}

	// Compare by section name
	aSectionName := ""
	if a.SectionName != nil {
		aSectionName = string(*a.SectionName)
	}
	bSectionName := ""
	if b.SectionName != nil {
		bSectionName = string(*b.SectionName)
	}
	return strings.Compare(aSectionName, bSectionName)
}

func compareHTTPRouteRule(a, b gatewayv1.HTTPRouteRule) int {
	// Compare by first match's path value if exists
	aPath := ""
	if len(a.Matches) > 0 && a.Matches[0].Path != nil && a.Matches[0].Path.Value != nil {
		aPath = *a.Matches[0].Path.Value
	}
	bPath := ""
	if len(b.Matches) > 0 && b.Matches[0].Path != nil && b.Matches[0].Path.Value != nil {
		bPath = *b.Matches[0].Path.Value
	}
	if cmp := strings.Compare(aPath, bPath); cmp != 0 {
		return cmp
	}

	// Compare by first backend's name if exists
	aBackend := ""
	if len(a.BackendRefs) > 0 {
		aBackend = string(a.BackendRefs[0].Name)
	}
	bBackend := ""
	if len(b.BackendRefs) > 0 {
		bBackend = string(b.BackendRefs[0].Name)
	}
	return strings.Compare(aBackend, bBackend)
}
