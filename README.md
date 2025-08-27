# ingress2httproute

A Kubernetes controller that automatically converts Ingress resources to Gateway API HTTPRoute resources, enabling migration from Ingress to the Gateway API.

## Description

This controller watches for Ingress resources and creates corresponding HTTPRoute resources following a **"One Host = One HTTPRoute"** mapping pattern. Each unique hostname in an Ingress results in a separate HTTPRoute resource, ensuring clean separation and predictable behavior.

### Mapping Rules

- **One HTTPRoute per hostname**: Each host in an Ingress spec creates a separate HTTPRoute
- **Gateway selection**: HTTPRoutes automatically attach to Gateways that match the hostname (exact match, wildcard, or catch-all)
- **Path-based routing**: All paths for a given hostname are consolidated into rules within that hostname's HTTPRoute
- **Backend services**: Service backends are mapped with full reference details (group, kind, namespace, port, weight)
- **Cross-namespace support**: HTTPRoutes can reference Gateways in different namespaces when allowed by the Gateway
- **Owner references**: Created HTTPRoutes have owner references to their source Ingress for automatic cleanup

### Important Limitations

⚠️ **Default backends are explicitly NOT supported**. The `spec.defaultBackend` field in Ingress resources is ignored. Default backends make the mapping complex and can have unwanted side effects when multiple HTTPRoutes interact with the same Gateway listeners. If you need catch-all behavior, configure it explicitly using path-based rules.

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

