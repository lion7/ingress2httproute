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
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1 "k8s.io/api/networking/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	timeout     = time.Second * 10
	interval    = time.Millisecond * 250
	testdataDir = "../../testdata"
)

var _ = Describe("Ingress Controller", func() {
	ctx := context.Background()

	// Helper function to load multiple objects from a multi-doc YAML file
	loadMultipleFromYAML := func(filePath string) ([]ctrlclient.Object, error) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}

		var objects []ctrlclient.Object
		decode := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer().Decode

		// Split by YAML document separator
		docs := strings.Split(string(data), "---")
		for _, doc := range docs {
			doc = strings.TrimSpace(doc)
			if doc == "" {
				continue
			}

			obj, _, err := decode([]byte(doc), nil, nil)
			if err != nil {
				return nil, err
			}
			if clientObj, ok := obj.(ctrlclient.Object); ok {
				objects = append(objects, clientObj)
			}
		}

		return objects, nil
	}

	// Helper function to apply objects to the cluster
	applyObjects := func(objects []ctrlclient.Object) []ctrlclient.Object {
		var appliedObjects []ctrlclient.Object
		for _, obj := range objects {
			err := k8sClient.Create(ctx, obj)
			if err != nil {
				Fail(fmt.Sprintf("Failed to create %T: %v", obj, err))
			} else {
				appliedObjects = append(appliedObjects, obj)
			}
		}
		return appliedObjects
	}

	// Helper function to find Ingress from loaded objects
	findIngress := func(objects []ctrlclient.Object) *networkingv1.Ingress {
		for _, obj := range objects {
			if ingress, ok := obj.(*networkingv1.Ingress); ok {
				return ingress
			}
		}
		return nil
	}

	// Helper function to find HTTPRoutes from loaded objects
	findHTTPRoutes := func(objects []ctrlclient.Object) []*gatewayv1.HTTPRoute {
		var routes []*gatewayv1.HTTPRoute
		for _, obj := range objects {
			if route, ok := obj.(*gatewayv1.HTTPRoute); ok {
				routes = append(routes, route)
			}
		}
		return routes
	}

	// Helper function to validate HTTPRoute against expected
	validateHTTPRouteAgainstExpected := func(actual *gatewayv1.HTTPRoute, expected *gatewayv1.HTTPRoute) {
		By(fmt.Sprintf("validating HTTPRoute %s matches expected", actual.Name))

		// Validate hostnames
		if len(expected.Spec.Hostnames) > 0 {
			Expect(actual.Spec.Hostnames).To(HaveLen(len(expected.Spec.Hostnames)))
			for i, expectedHostname := range expected.Spec.Hostnames {
				Expect(actual.Spec.Hostnames[i]).To(Equal(expectedHostname))
			}
		} else {
			// For catch-all routes, hostnames should be empty
			Expect(actual.Spec.Hostnames).To(BeEmpty())
		}

		// Validate parent references structure
		Expect(actual.Spec.ParentRefs).To(HaveLen(len(expected.Spec.ParentRefs)))
		for i, expectedParentRef := range expected.Spec.ParentRefs {
			actualParentRef := actual.Spec.ParentRefs[i]
			Expect(actualParentRef.Group).To(Equal(expectedParentRef.Group))
			Expect(actualParentRef.Kind).To(Equal(expectedParentRef.Kind))
			Expect(actualParentRef.Name).To(Equal(expectedParentRef.Name))

			if expectedParentRef.Namespace != nil {
				Expect(actualParentRef.Namespace).NotTo(BeNil())
				Expect(*actualParentRef.Namespace).To(Equal(*expectedParentRef.Namespace))
			}
		}

		// Validate rules structure
		Expect(actual.Spec.Rules).To(HaveLen(len(expected.Spec.Rules)))

		// Validate first rule (most tests have single rule)
		if len(expected.Spec.Rules) > 0 {
			expectedRule := expected.Spec.Rules[0]
			actualRule := actual.Spec.Rules[0]

			// Validate matches
			Expect(actualRule.Matches).To(HaveLen(len(expectedRule.Matches)))

			// Validate backend references
			Expect(actualRule.BackendRefs).To(HaveLen(len(expectedRule.BackendRefs)))
			for i, expectedBackendRef := range expectedRule.BackendRefs {
				actualBackendRef := actualRule.BackendRefs[i]
				Expect(actualBackendRef.Name).To(Equal(expectedBackendRef.Name))
				if expectedBackendRef.Port != nil {
					Expect(actualBackendRef.Port).NotTo(BeNil())
					Expect(*actualBackendRef.Port).To(Equal(*expectedBackendRef.Port))
				}
			}
		}

		// Validate owner references
		Expect(actual.OwnerReferences).To(HaveLen(1))
		Expect(actual.OwnerReferences[0].Kind).To(Equal("Ingress"))
		Expect(*actual.OwnerReferences[0].Controller).To(BeTrue())
	}

	// Helper function to cleanup resources
	cleanupTestResources := func(appliedResources []ctrlclient.Object) {
		// Clean up all applied resources
		for _, obj := range appliedResources {
			_ = k8sClient.Delete(ctx, obj)
		}

		// Clean up any HTTPRoutes that were created
		routeList := &gatewayv1.HTTPRouteList{}
		err := k8sClient.List(ctx, routeList)
		if err == nil {
			for _, route := range routeList.Items {
				_ = k8sClient.Delete(ctx, &route)
			}
		}
	}

	// Get test directories dynamically from testdata directory
	getTestDirectories := func() []string {
		entries, err := os.ReadDir(testdataDir)
		if err != nil {
			Fail(fmt.Sprintf("Failed to read testdata directory %s: %v", testdataDir, err))
			return nil
		}

		var testDirs []string
		for _, entry := range entries {
			if entry.IsDir() && (strings.HasPrefix(entry.Name(), "0") || strings.HasPrefix(entry.Name(), "1")) { // Test directories starting with 0 or 1
				testDirs = append(testDirs, entry.Name())
			}
		}
		return testDirs
	}

	testDirectories := getTestDirectories()
	for _, testDir := range testDirectories {
		tc := testDir // capture loop variable

		Context(tc, func() {
			var (
				ingress            *networkingv1.Ingress
				appliedResources   []ctrlclient.Object
				expectedHTTPRoutes []*gatewayv1.HTTPRoute
				defaultResources   []ctrlclient.Object
			)

			BeforeEach(func() {
				By(fmt.Sprintf("Setting up test case: %s", tc))

				// Load and apply default resources
				defaultPath := filepath.Join(testdataDir, "default.yaml")
				defaultObjs, err := loadMultipleFromYAML(defaultPath)
				if err != nil {
					Fail(fmt.Sprintf("Failed to load default.yaml: %v", err))
				}
				defaultResources = applyObjects(defaultObjs)

				// Load input objects from input.yaml
				inputPath := filepath.Join(testdataDir, tc, "input.yaml")
				inputObjs, err := loadMultipleFromYAML(inputPath)
				if err != nil {
					Fail(fmt.Sprintf("Failed to load input.yaml: %v", err))
				}

				// Find the Ingress from input objects
				ingress = findIngress(inputObjs)
				Expect(ingress).NotTo(BeNil(), "Ingress should exist in input.yaml")

				// Apply all input objects
				appliedResources = applyObjects(inputObjs)

				// Load expected HTTPRoutes from output.yaml
				outputPath := filepath.Join(testdataDir, tc, "output.yaml")
				outputObjs, err := loadMultipleFromYAML(outputPath)
				if err != nil {
					Fail(fmt.Sprintf("Failed to load output.yaml: %v", err))
				}
				expectedHTTPRoutes = findHTTPRoutes(outputObjs)

				// Note: Some test cases (like 10-no-hostname-rules) may have no expected HTTPRoutes
				if tc != "10-no-hostname-rules" {
					Expect(expectedHTTPRoutes).NotTo(BeEmpty(), "At least one expected HTTPRoute should exist in output.yaml")
				}
			})

			AfterEach(func() {
				By("Cleaning up test resources")
				cleanupTestResources(appliedResources)
				cleanupTestResources(defaultResources)

				// Reset for next test
				appliedResources = nil
				defaultResources = nil
				expectedHTTPRoutes = nil
				ingress = nil
			})

			It("should handle ingress to httproute mapping correctly", func() {
				By("Reconciling the Ingress")
				reconciler := &IngressReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      ingress.Name,
						Namespace: ingress.Namespace,
					},
				})
				Expect(err).NotTo(HaveOccurred())

				// Wait for controller to process
				time.Sleep(2 * time.Second)

				// List all HTTPRoutes in the ingress namespace
				By("Listing all HTTPRoutes in the ingress namespace")
				routeList := &gatewayv1.HTTPRouteList{}

				// Wait for the expected number of HTTPRoutes to be created
				Eventually(func() int {
					err := k8sClient.List(ctx, routeList, ctrlclient.InNamespace(ingress.Namespace))
					if err != nil {
						return -1
					}
					return len(routeList.Items)
				}, timeout, interval).Should(Equal(len(expectedHTTPRoutes)),
					fmt.Sprintf("Should have exactly %d HTTPRoutes in namespace %s", len(expectedHTTPRoutes), ingress.Namespace))

				// Get the final list of routes
				err = k8sClient.List(ctx, routeList, ctrlclient.InNamespace(ingress.Namespace))
				Expect(err).NotTo(HaveOccurred())

				// Validate each created HTTPRoute matches one of the expected ones
				By(fmt.Sprintf("Validating %d created HTTPRoutes match expected", len(routeList.Items)))
				for i := range routeList.Items {
					actualRoute := &routeList.Items[i]

					// Find the matching expected route (by hostname or other characteristics)
					var matchedExpected *gatewayv1.HTTPRoute
					for _, expected := range expectedHTTPRoutes {
						// Try to match by hostnames (or lack thereof for catch-all routes)
						if len(actualRoute.Spec.Hostnames) == len(expected.Spec.Hostnames) {
							if len(actualRoute.Spec.Hostnames) == 0 {
								// Both are catch-all routes
								matchedExpected = expected
								break
							} else if len(actualRoute.Spec.Hostnames) > 0 &&
								actualRoute.Spec.Hostnames[0] == expected.Spec.Hostnames[0] {
								// Hostnames match
								matchedExpected = expected
								break
							}
						}
					}

					Expect(matchedExpected).NotTo(BeNil(),
						fmt.Sprintf("HTTPRoute with hostnames %v should have a matching expected route", actualRoute.Spec.Hostnames))

					// Validate against the matched expected route
					validateHTTPRouteAgainstExpected(actualRoute, matchedExpected)
				}
			})
		})
	}
})
