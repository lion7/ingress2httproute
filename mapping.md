# Ingress vs. Gateway API – Mapping Specification

## Overview and Context

Kubernetes **Ingress** and the newer **Gateway API** both provide ways to expose Services via HTTP/S routing, but they differ in scope and design.

* **Ingress**: A single resource that combines *load balancer entry point* and *routing rules*.
* **Gateway API**: Separates responsibilities into multiple resources (GatewayClass, Gateway, HTTPRoute, etc.)  .

This separation enables more flexible deployments (shared gateways, independent route objects).
We are implementing a controller that **converts Ingress resources into HTTPRoute resources** under the Gateway API model.

---

## Functional Requirements

* **Ingress to HTTPRoute Conversion**: For each Ingress, generate one or more HTTPRoutes with equivalent routing behavior .
* **Hostname-Based Gateway Selection**: Select a Gateway by matching Ingress hostnames to Gateway listener hostnames or wildcards .
* **Ignore Non-Host Rules**: Skip Ingress rules without a hostname (to avoid ambiguous routing).
* **Path Rule Conversion**: Map Ingress paths and path types (`Prefix`, `Exact`, `ImplementationSpecific`) to HTTPRoute equivalents.
* **Default Backend Support**: Translate Ingress `defaultBackend` into a catch-all HTTPRoute.
* **TLS Handling**: Gateway (listeners) owns TLS config; HTTPRoute attaches by hostname.
* **No Duplicate Routes**: Prevent ambiguous overlapping HTTPRoutes on the same Gateway.

---

## Mapping Logic and Behavior

### 1. Gateway Selection by Hostname

* **Discovery**: Controller inspects Gateways for listeners whose `hostname` matches an Ingress host (exact, wildcard, or none = all hosts).
* **Priority**: Favor most specific match (exact > wildcard > catch-all).
* **Binding**: Add `parentRefs` in HTTPRoute to attach to Gateway.
* **Multiple Listeners**: If both HTTP (80) and HTTPS (443) exist, attach to both.
* **IngressClass vs. Gateway**: Controller config defines which Gateway(s) an Ingress class maps to .

### 2. HTTPRoute Creation per Host

* **One Host = One HTTPRoute**: Create separate HTTPRoutes per Ingress host  .
* **Naming**: Derive from Ingress name + host.
* **Hostnames Field**: Populate with Ingress host.
* **ParentRefs**: Reference the selected Gateway (optionally with listener `sectionName`).
* **AllowedRoutes**: Ensure namespace compatibility (default = same namespace) .

### 3. Path and Rule Translation

* **Path Matching**:

    * `Prefix` → `PathPrefix`
    * `Exact` → `PathExact`
    * `ImplementationSpecific` → treat as Prefix (optionally regex if supported).
* **Rule Mapping**: One HTTPRoute rule per Ingress path.
* **BackendRefs**: Map to Services and ports  .
* **Conflict Resolution**: Gateway API enforces deterministic longest-prefix/most-specific match .

### 4. Default Backend Handling

* **Ingress Semantics**: A fallback Service for non-matching requests  .
* **Gateway API**: No native default; must emulate with a catch-all route .
* **Mapping**:

    * Create HTTPRoute with no `hostnames` + path `/` prefix.
    * Attach to Gateway listener with wildcard or empty hostname.
* **Conflicts**: Only one default HTTPRoute can meaningfully exist per Gateway  .
* **Strategy**: Either consolidate into one shared default route or only map when Ingress has *only* a defaultBackend.

---

## Technical Details

### Resource Mapping Summary

| Ingress Field/Concept              | Gateway API Equivalent                       |
| ---------------------------------- | -------------------------------------------- |
| `ingressClassName`                 | Gateway selection                            |
| `rules[].host`                     | HTTPRoute `hostnames`                        |
| `rules[].http.paths`               | HTTPRoute `rules.matches`                    |
| `pathType: Prefix`                 | `PathPrefix`                                 |
| `pathType: Exact`                  | `PathExact`                                  |
| `pathType: ImplementationSpecific` | Default to `PathPrefix` (regex if supported) |
| Backend (service/port)             | HTTPRoute `backendRefs`                      |
| `defaultBackend`                   | HTTPRoute catch-all                          |
| TLS (`spec.tls`)                   | Gateway listener TLS                         |

### Example

*Ingress:*

```yaml
spec:
  ingressClassName: prod-class
  rules:
  - host: app.example.com
    http:
      paths:
      - path: /api
        pathType: Prefix
        backend:
          service:
            name: api-service
            port:
              number: 80
      - path: /admin
        pathType: Exact
        backend:
          service:
            name: admin-service
            port:
              number: 80
  - host: static.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: web-static
            port:
              number: 80
```

*Gateway (assumed):*

```yaml
spec:
  gatewayClassName: prod-class
  listeners:
  - name: http
    protocol: HTTP
    port: 80
    hostname: "*.example.com"
  - name: https
    protocol: HTTPS
    port: 443
    hostname: "*.example.com"
```

*Generated HTTPRoutes:*

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: app-example-com
spec:
  parentRefs:
  - name: example-gw
  hostnames:
  - "app.example.com"
  rules:
  - matches:
    - path: { type: PathPrefix, value: /api }
    backendRefs: [{ name: api-service, port: 80 }]
  - matches:
    - path: { type: PathExact, value: /admin }
    backendRefs: [{ name: admin-service, port: 80 }]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: static-example-com
spec:
  parentRefs:
  - name: example-gw
  hostnames:
  - "static.example.com"
  rules:
  - matches:
    - path: { type: PathPrefix, value: / }
    backendRefs: [{ name: web-static, port: 80 }]
```

---

## Conclusion

Key differences between Ingress and Gateway API for controller implementation:

* **Gateway Selection**: Match Ingress hosts to Gateway listeners.
* **Rules**: Map paths with type-aware conversion.
* **Default Backend**: Must be emulated as a catch-all route.
* **Listener Binding**: Determined by hostnames, ports, and protocols automatically.

This mapping ensures Ingress resources are faithfully represented as HTTPRoutes and prepares the cluster for future use of Gateway API features.

---

**Sources:**

* Kubernetes Gateway API – Ingress migration guide
* Kubernetes Gateway API – HTTPRoute spec
* Kubernetes Gateway API – Routing and conflicts
* Kubernetes Ingress API – Default backend semantics

---

Do you want me to also include a **diagram** (e.g., Ingress → Gateway → HTTPRoute mapping flow) in the Markdown? That could help visualize how your controller translates resources.
