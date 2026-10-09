# k8s_gateway

A CoreDNS plugin that is very similar to [k8s_external](https://coredns.io/plugins/k8s_external/) but supporting all types of Kubernetes external resources - Ingress, Service of type LoadBalancer, HTTPRoutes, TLSRoutes, GRPCRoutes from the [Gateway API project](https://gateway-api.sigs.k8s.io/).

This plugin relies on its own connection to the k8s API server and doesn't share any code with the existing [kubernetes](https://coredns.io/plugins/kubernetes/) plugin. The assumption is that this plugin can now be deployed as a separate instance (alongside the internal kube-dns) and act as a single external DNS interface into your Kubernetes cluster(s).

## Supported resources

| Kind | Names are taken from | Addresses are taken from |
| ---- | -------------------- | ------------------------ |
| Ingress | `spec.rules[*].host` | `.status.loadBalancer.ingress` |
| Service | `name.namespace` plus the configured zones, or the `coredns.io/hostname` / `external-dns.kubernetes.io/hostname` (or legacy `external-dns.alpha.kubernetes.io/hostname`) annotations | `.status.loadBalancer.ingress`, or ready EndpointSlice addresses when endpoint resolution is enabled |
| HTTPRoute | `spec.hostnames` and `spec.parentRefs` | The referenced Gateway's `status.addresses` |
| TLSRoute | `spec.hostnames` and `spec.parentRefs` | The referenced Gateway's `status.addresses` |
| GRPCRoute | `spec.hostnames` and `spec.parentRefs` | The referenced Gateway's `status.addresses` |
| DNSEndpoint | `spec.endpoints[*].dnsName` | `spec.endpoints[*].targets` |

> [!IMPORTANT] Gateway API routes require Gateway API CRDs v1.1.0 or newer from the experimental channel.
> Currently, supports A and AAAA-type queries, all other queries result in NODATA responses.

> This plugin is **NOT** supposed to be used for intra-cluster DNS resolution and does not contain the default upstream [kubernetes](https://coredns.io/plugins/kubernetes/) plugin.

Every supported route can use a Gateway or a ListenerSet as its `spec.parentRefs` parent. ListenerSets are resolved through their parent Gateway, and the Gateway's `status.addresses` are used for DNS responses. TLSRoute and GRPCRoute require the `v1` API version. DNSEndpoint requires the external-dns CRDs.

## Install with Helm

The supported deployment method is the Helm chart:

```bash
helm install k8s-gateway oci://codeberg.org/k8s-gateway/charts/k8s-gateway
```

The chart creates the Deployment, Service, ServiceAccount, and resource-scoped RBAC required by the configured plugins. RBAC is controlled by `rbac.create`. The chart also supports HPA, PodDisruptionBudget, Prometheus ServiceMonitor, multiple protocols, probes, security contexts, and additional volumes.

See [`chart/README.md`](chart/README.md) for the complete chart values table.

## Chart configuration

The chart uses CoreDNS server blocks. Configure the zones and plugins under `servers`; there are no chart-level `domain`, `watchedResources`, `filters`, `ttl`, or `apex` values.

This is a minimal working `values.yaml`:

```yaml
servers:
  - zones:
      - zone: example.com
    port: 53
    plugins:
      - name: errors
      - name: health
        configBlock: |-
          lameduck 10s
      - name: ready
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          resources Ingress Service
          loadBalancerAddressPreference ip
      - name: forward
        parameters: . /etc/resolv.conf
      - name: cache
        parameters: 30
      - name: reload
      - name: loadbalance
```

Each server entry can define:

* `zones`: one or more zones. A zone can set `zone`, `scheme`, and `use_tcp`. Supported schemes are `dns://`, `tls://`, `https://`, and `grpc://`.
* `port`: the listener port for that server block.
* `plugins`: CoreDNS plugins. Each plugin supports `name`, optional
  `parameters`, and optional `configBlock`.

> [!WARNING] The plugin name is significant: the Kubernetes resource resolver must be configured exactly as `name: k8s_gateway`. Renaming this entry causes the chart to generate a CoreDNS configuration that does not load this plugin.

Chart-level settings include:

* `serviceType` and `service`: Service type and networking options.
* `replicaCount`, `resources`, `affinity`, `nodeSelector`, `tolerations`, and `topologySpreadConstraints`: workload scheduling and sizing.
* `serviceAccount` and `rbac`: identity and permissions.
* `livenessProbe`, `readinessProbe`, `securityContext`, and `podSecurityContext`: pod health and security.
* `prometheus.service` and `prometheus.monitor`: metrics Service and ServiceMonitor.
* `hpa` and `podDisruptionBudget`: availability and scaling.
* `extraConfig`, `zoneFiles`, `extraContainers`, `extraVolumes`, `extraVolumeMounts`, `extraSecrets`, `env`, and `initContainers`: additional CoreDNS or pod configuration.

## `k8s_gateway` plugin configuration

The plugin is configured inside a server's `plugins` list. Keep the plugin name exactly `k8s_gateway`; changing it will prevent the plugin from working:

```yaml
- name: k8s_gateway
  parameters: example.com
  configBlock: |-
    resources Ingress Service HTTPRoute
    ingressClasses nginx
    gatewayClasses external
    serviceLabelSelectors app=public
    loadBalancerAddressPreference ip
    ttl 60
    apex dns.example.com
    secondary dns-secondary.example.com
    fallthrough in-addr.arpa ip6.arpa
```

The plugin parameters and directives are:

* `parameters`: authoritative zones, for example `example.com`.
* `resources`: resources to watch: `Ingress`, `Service`, `HTTPRoute`, `TLSRoute`, `GRPCRoute`, and `DNSEndpoint`. If omitted, `Ingress` and `Service` are watched.
* `ingressClasses`: restrict Ingresses by `ingressClassName`.
* `gatewayClasses`: restrict Gateway API resources by `gatewayClassName`.
* `serviceLabelSelectors`: one or more Kubernetes label selectors. Each selector creates a watch and the results are merged.
* `loadBalancerAddressPreference`: `hostname` (default) or `ip`. This applies to Service and Ingress LoadBalancer status entries. `hostname` prefers a resolvable hostname; `ip` prefers a valid status IP and falls back to the hostname.
* `ttl`: response TTL. The default is 60 seconds.
* `apex`: overrides the generated apex record.
* `secondary`: adds a peer nameserver apex record.
* `kubeconfig`: path to a kubeconfig for a remote cluster, optionally followed by a context name.
* `fallthrough`: passes unanswered queries to the next plugin. With no zones, it applies to every authoritative zone; with zones, it applies only to those zones.

## Ignoring resources

Add `k8s-gateway.dns/ignore: "true"` to a supported resource to exclude it:

```yaml
metadata:
  labels:
    k8s-gateway.dns/ignore: "true"
```

This works for Ingress, Service, HTTPRoute, TLSRoute, GRPCRoute, and DNSEndpoint resources.

## Endpoint resolution

Services normally resolve to `.status.loadBalancer.ingress`. To return ready pod addresses from EndpointSlices instead, add:

```yaml
metadata:
  annotations:
    k8s-gateway.dns/resolve-endpoints: "true"
```

Endpoint resolution is opt-in and works for LoadBalancer, ClusterIP, and headless Services. Only ready endpoints are returned, including both IPv4 and IPv6 addresses when available. The chart automatically grants EndpointSlice permissions when a configured server watches `Service`.

Service hostnames can still be supplied with an annotation:

```yaml
metadata:
  annotations:
    external-dns.kubernetes.io/hostname: app.example.com
```

The legacy `external-dns.alpha.kubernetes.io/hostname` annotation is still supported. When several are set, `coredns.io/hostname` takes precedence, followed by `external-dns.kubernetes.io/hostname`, then the legacy key. Multiple hostnames can be comma-separated.

## Multiple nameservers

For deployments that require two authoritative nameservers, install two chart releases with separate LoadBalancer Services. Leave `apex` unset so each release uses its generated name, and set the other release as `secondary`.

For example, the `k8s_gateway` plugin configuration in the first release can be:

```yaml
servers:
  - zones:
      - zone: example.com
    plugins:
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          secondary exdns-2-k8s-gateway.k8s-gateway
```

Use the corresponding `exdns-1-k8s-gateway.k8s-gateway` value in the second release, then create the required NS and glue records for both Service addresses.

## Development

The repository includes a Tilt development environment backed by kind:

```bash
make setup
make up
```

Some test resources can be added to the k8s cluster with:

```bash
# ingress and service resources
kubectl apply -f ./test/single-stack/ingress-services.yml

# gateway API resources
kubectl apply -f ./test/gateway-api/resources.yml

# DNSEndpoint resources
kubectl apply -f ./test/dnsendpoint.yaml
```

The chart's default test configuration exposes DNS on the NodePort used by the test environment. Query it with:

```bash
ip=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[0].address}')
dig @$ip -p 32553 myservicea.foo.org +short
```

Clean up the local environment with:

```bash
make nuke
```
