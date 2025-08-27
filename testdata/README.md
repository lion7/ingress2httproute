# Ingress to HTTPRoute Mapping Test Suite

This directory contains comprehensive test cases for validating the Ingress-to-HTTPRoute mapping controller against the specification defined in `mapping.txt`.

## Test Case Structure

Each test case is organized in its own directory with the following structure:
- `actual.yaml` - The input Ingress resource
- `expected*.yaml` - The expected HTTPRoute resource(s) that should be created
- `gateway.yaml` (optional) - Example Gateway resources for the test scenario
- `service.yaml` (optional) - Example Service resources referenced by the Ingress

## Test Cases Overview

### Basic Functionality
- **01-simple-single-host** - Basic single host with single path
- **02-single-host-multiple-paths** - Single host with multiple paths and different backends
- **03-multiple-hosts** - Multiple hosts that should create separate HTTPRoutes
- **11-service-port-by-name** - Service references using port names instead of numbers

### Path Type Mapping
- **04-path-types** - Tests all Ingress path types (Prefix, Exact, ImplementationSpecific)

### Default Backend Handling  
- **05-default-backend-with-hosts** - Default backend combined with host-specific rules
- **06-default-backend-only** - Ingress with only default backend (no host rules)

### Hostname Matching
- **07-wildcard-hostnames** - Wildcard hostname matching scenarios
- **13-catch-all-gateway** - Gateway with no hostname restriction (catch-all)
- **14-gateway-priority** - Multiple Gateways with different specificity levels

### TLS Configuration
- **08-tls-configuration** - TLS certificates and HTTPS listeners

### Advanced Features
- **09-resource-backend** - Non-Service backends (custom resources)
- **10-no-hostname-rules** - Rules without hostnames (should be filtered out)
- **12-cross-namespace** - Cross-namespace Gateway references

## Running Tests

To test your controller implementation:

1. Apply the Gateway resources from each test case directory
2. Apply the `actual.yaml` Ingress resource 
3. Verify that the controller creates HTTPRoute resources matching the `expected*.yaml` files
4. Check that the HTTPRoute resources have correct:
   - Metadata (name, namespace, ownerReferences)
   - Spec (parentRefs, hostnames, rules, backendRefs)
   - Status conditions

## Key Validation Points

### Critical Spec Requirements to Test:
- ✅ **One HTTPRoute per hostname** - Multiple hosts should create separate HTTPRoutes
- ✅ **Default backend creates catch-all route** - Separate HTTPRoute with no hostnames
- ✅ **Hostname matching logic** - Exact > Wildcard > Catch-all priority
- ✅ **Path type mapping** - ImplementationSpecific → PathPrefix
- ✅ **Cross-namespace support** - HTTPRoute in Ingress namespace, references Gateway in different namespace
- ✅ **Hostname-less rules filtered out** - Rules without host should be ignored (except default backend)

### Common Issues to Watch For:
- **Single HTTPRoute with multiple hostnames** - Should create separate HTTPRoutes per host
- **Default backend added to path rules** - Should be separate catch-all HTTPRoute
- **Missing catch-all Gateway listeners** - Gateways with no hostname should be considered
- **Wrong PathType mapping** - ImplementationSpecific should map to PathPrefix, not RegularExpression
- **Broken wildcard matching** - `*.example.com` should match `api.example.com`

## Expected Controller Behavior

Based on the mapping specification, the controller should:

1. **Filter out hostname-less rules** (except when handling default backend)
2. **Create one HTTPRoute per unique hostname** from Ingress rules
3. **Select most specific Gateway listener** for each hostname
4. **Create separate catch-all HTTPRoute** for default backends
5. **Map path types correctly** (Prefix→PathPrefix, Exact→PathExact, ImplementationSpecific→PathPrefix)
6. **Resolve service port names** to port numbers in HTTPRoute backendRefs
7. **Handle cross-namespace Gateway references** properly
8. **Set proper owner references** for garbage collection

## Test Validation Script

```bash
#!/bin/bash
# Example validation script structure

for test_dir in testdata/*/; do
    echo "Testing $(basename "$test_dir")"
    
    # Apply Gateway and Service resources if they exist
    [ -f "$test_dir/gateway.yaml" ] && kubectl apply -f "$test_dir/gateway.yaml"
    [ -f "$test_dir/service.yaml" ] && kubectl apply -f "$test_dir/service.yaml"
    
    # Apply Ingress
    kubectl apply -f "$test_dir/actual.yaml"
    
    # Wait for controller to reconcile
    sleep 5
    
    # Validate HTTPRoutes match expected
    for expected_file in "$test_dir"/expected*.yaml; do
        [ -f "$expected_file" ] || continue
        echo "Validating $expected_file"
        # Add validation logic here
    done
    
    # Cleanup
    kubectl delete -f "$test_dir/actual.yaml" --ignore-not-found
    [ -f "$test_dir/gateway.yaml" ] && kubectl delete -f "$test_dir/gateway.yaml" --ignore-not-found
    [ -f "$test_dir/service.yaml" ] && kubectl delete -f "$test_dir/service.yaml" --ignore-not-found
done
```

## Notes

- **UIDs in expected files** are placeholder values (`12345678-1234-1234-1234-123456789012`)
- **Gateway references** assume specific Gateway names - adjust based on your test environment
- **Namespace consistency** - Most tests use `demo` namespace for simplicity
- **Weight values** - All backendRefs use `weight: 1` as per Gateway API defaults