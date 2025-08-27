# ingress2httproute

A Kubernetes controller that enables **mounting Ingress resources onto existing Gateway API infrastructure**, providing a bridge between traditional Ingress simplicity and Gateway API power.

## Overview

Unlike full migration tools, `ingress2httproute` implements a **"mounting" strategy** that allows application developers to continue using familiar Ingress resources while leveraging pre-configured Gateway infrastructure managed by cluster administrators.

### Key Features

- **🔄 Non-Invasive**: Works with existing Gateway infrastructure without modifications
- **🎯 Hostname-Based Selection**: Automatically discovers and attaches to appropriate Gateways based on hostname matching
- **👥 Role Separation**: Clear division between infrastructure (administrators) and application routing (developers)
- **🔄 Live Reconciliation**: Continuous synchronization of Ingress changes to HTTPRoute resources
- **🧹 Automatic Cleanup**: Owner references ensure HTTPRoutes are cleaned up when Ingress resources are deleted

### Architecture Philosophy

```
┌─────────────────────┐    ┌──────────────────────┐    ┌─────────────────────┐
│   Cluster Admin     │    │    Controller        │    │  App Developer      │
│                     │    │                      │    │                     │
│ • Gateway Resources │───▶│ • Gateway Discovery  │◀───│ • Ingress Resources │
│ • TLS Configuration │    │ • HTTPRoute Creation │    │ • Familiar Workflow │
│ • Infrastructure    │    │ • Hostname Matching  │    │ • No Gateway API    │
└─────────────────────┘    └──────────────────────┘    └─────────────────────┘
```

### What This Controller Handles

- ✅ **HTTPRoute Generation**: One HTTPRoute per Ingress hostname
- ✅ **Gateway Discovery**: Automatic selection based on hostname patterns (exact, wildcard, catch-all)
- ✅ **Path Type Conversion**: Complete support for Prefix, Exact, and ImplementationSpecific paths
- ✅ **Backend Translation**: Service and resource backend references with port resolution
- ✅ **Cross-Namespace Routes**: Respects Gateway AllowedRoutes configuration
- ✅ **Resource Management**: Proper ownership, updates, and garbage collection

### What Is Deliberately Not Handled

- ❌ **Gateway Creation**: Uses existing Gateway resources only
- ❌ **TLS Management**: TLS configuration remains at Gateway level  
- ❌ **Default Backends**: Ingress `defaultBackend` specifications are ignored
- ❌ **IngressClass Mapping**: Uses hostname-based Gateway selection instead

### Design Rationale

This controller enables **gradual Gateway API adoption** by:
- Preserving developer experience with Ingress resources
- Maintaining administrator control over network infrastructure
- Providing a safe experimentation path for Gateway API features
- Supporting mixed environments during transition periods

> 📖 **For complete design details, architecture decisions, and implementation principles, see [design.md](./design.md)**

### Comparison with ingress2gateway

| Feature | ingress2httproute | [kubernetes-sigs/ingress2gateway](https://github.com/kubernetes-sigs/ingress2gateway) |
|---------|-------------------|-------------------------|
| **Purpose** | Mount Ingress on existing Gateways | Complete migration to Gateway API |
| **Gateway Management** | Uses existing infrastructure | Creates new Gateway resources |
| **TLS Support** | Delegated to Gateway administrators | Full TLS configuration generation |
| **Default Backends** | Not supported | Fully supported |
| **Runtime Model** | Live controller | CLI conversion tool |
| **Use Case** | Gradual adoption, infrastructure control | Full migration, self-service model |

## Getting Started

### Prerequisites
- go version v1.22.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/ingress2httproute:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/ingress2httproute:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following are the steps to build the installer and distribute this project to users.

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/ingress2httproute:tag
```

NOTE: The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without
its dependencies.

2. Using the installer

Users can just run kubectl apply -f <URL for YAML BUNDLE> to install the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/ingress2httproute/<tag or branch>/dist/install.yaml
```

## Contributing

Contributions are welcome! Please feel free to submit pull requests or open issues for bugs and feature requests.

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

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

