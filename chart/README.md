# k8s-gateway

[k8s_gateway](https://codeberg.org/k8s-gateway/k8s_gateway) is a CoreDNS plugin that resolves DNS from Kubernetes resources (Ingress, Service, HTTPRoute, TLSRoute, GRPCRoute, etc.).

## Introduction

This chart bootstraps a [k8s_gateway](https://codeberg.org/k8s-gateway/k8s_gateway) deployment on a [Kubernetes](http://kubernetes.io) cluster using the [Helm](https://helm.sh) package manager. It acts as an external DNS server, resolving DNS queries based on your Kubernetes resources.

## Prerequisites

- Kubernetes 1.19 or later

## Installing the Chart

The chart can be installed as follows:

```bash
helm install k8s-gateway oci://codeberg.org/k8s-gateway/charts/k8s-gateway
```

The command deploys k8s-gateway on the Kubernetes cluster in the default configuration. The [configuration](#configuration) section lists various ways to override default configuration during deployment.

## Uninstalling the Chart

To uninstall/delete the `k8s-gateway` deployment:

```bash
helm uninstall k8s-gateway
```

The command removes all the Kubernetes components associated with the chart and deletes the release.

## Configuration

| Parameter | Description | Default |
| :-------- | :---------- | :------ |
| `image.repository` | Image repository | `codeberg.org/k8s-gateway/k8s_gateway` |
| `image.tag` | Image tag (derived from Chart.yaml appVersion) | `""` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `image.pullSecrets` | Specify container image pull secrets | `[]` |
| `replicaCount` | Number of replicas | `1` |
| `resources.limits.cpu` | Container maximum CPU | `100m` |
| `resources.limits.memory` | Container maximum memory | `128Mi` |
| `resources.requests.cpu` | Container requested CPU | `100m` |
| `resources.requests.memory` | Container requested memory | `128Mi` |
| `serviceType` | Kubernetes Service type (`LoadBalancer`, `NodePort`, `ClusterIP`) | `LoadBalancer` |
| `prometheus.service.enabled` | Set to `true` to create Service for Prometheus metrics | `false` |
| `prometheus.service.annotations` | Annotations to add to the metrics Service | `{prometheus.io/scrape: "true", prometheus.io/port: "9153"}` |
| `prometheus.service.selector` | Pod selector for metrics Service | `{}` |
| `prometheus.monitor.enabled` | Set to `true` to create ServiceMonitor for Prometheus operator | `false` |
| `prometheus.monitor.additionalLabels` | Additional labels for ServiceMonitor discovery | `{}` |
| `prometheus.monitor.namespace` | Namespace for ServiceMonitor | `""` |
| `prometheus.monitor.interval` | Scrape interval (e.g. `30s`) | `""` |
| `prometheus.monitor.scrapeTimeout` | Timeout for each scrape request | `""` |
| `prometheus.monitor.scheme` | HTTP scheme used for scraping | `""` |
| `prometheus.monitor.honorLabels` | Honor labels from the target when scraping | `false` |
| `prometheus.monitor.tlsConfig` | TLS configuration for the metrics endpoint | `{}` |
| `prometheus.monitor.relabelings` | Target relabeling rules applied before scraping | `[]` |
| `prometheus.monitor.metricRelabelings` | Metric relabeling rules applied after scraping | `[]` |
| `prometheus.monitor.selector` | Service selector for ServiceMonitor | `{}` |
| `service.clusterIP` | IP address to assign to service | `""` |
| `service.clusterIPs` | IP addresses to assign to service (dual-stack) | `[]` |
| `service.loadBalancerIP` | IP address to assign to load balancer (if supported) | `""` |
| `service.loadBalancerClass` | Load balancer class to use | `""` |
| `service.externalIPs` | External IP addresses | `[]` |
| `service.externalTrafficPolicy` | Enable client source IP preservation | `""` |
| `service.ipFamilyPolicy` | Service dual-stack policy | `""` |
| `service.annotations` | Annotations to add to service | `{}` |
| `service.selector` | Pod selector for service | `{}` |
| `service.trafficDistribution` | Service traffic routing strategy | `""` |
| `serviceAccount.create` | If true, create and use ServiceAccount | `true` |
| `serviceAccount.name` | ServiceAccount name (auto-generated if not set) | `""` |
| `serviceAccount.annotations` | Annotations on the ServiceAccount | `{}` |
| `rbac.create` | If true, create and use RBAC resources | `true` |
| `priorityClassName` | Name of Priority Class to assign pods | `""` |
| `podSecurityContext` | Pod-level security context | `{}` |
| `securityContext` | Container-level security context | `{}` |
| `servers` | Configuration for CoreDNS server blocks and plugins | See values.yml |
| `livenessProbe.enabled` | Enable/disable the Liveness probe | `true` |
| `livenessProbe.initialDelaySeconds` | Delay before liveness probe is initiated | `60` |
| `livenessProbe.periodSeconds` | How often to perform the probe | `10` |
| `livenessProbe.timeoutSeconds` | When the probe times out | `5` |
| `livenessProbe.failureThreshold` | Minimum consecutive failures for the probe to be considered failed | `5` |
| `livenessProbe.successThreshold` | Minimum consecutive successes for the probe to be considered successful | `1` |
| `readinessProbe.enabled` | Enable/disable the Readiness probe | `true` |
| `readinessProbe.initialDelaySeconds` | Delay before readiness probe is initiated | `30` |
| `readinessProbe.periodSeconds` | How often to perform the probe | `5` |
| `readinessProbe.timeoutSeconds` | When the probe times out | `5` |
| `readinessProbe.failureThreshold` | Minimum consecutive failures for the probe to be considered failed | `1` |
| `readinessProbe.successThreshold` | Minimum consecutive successes for the probe to be considered successful | `1` |
| `affinity` | Affinity settings for pod assignment | `{}` |
| `nodeSelector` | Node labels for pod assignment | `{}` |
| `tolerations` | Tolerations for pod assignment | `[]` |
| `topologySpreadConstraints` | Topology spread constraints | `[]` |
| `zoneFiles` | Configure custom zone files | `[]` |
| `extraContainers` | Optional array of sidecar containers | `[]` |
| `extraVolumes` | Optional array of extra volumes | `[]` |
| `extraVolumeMounts` | Optional array of extra volume mounts | `[]` |
| `extraSecrets` | Optional array of secrets to mount | `[]` |
| `extraConfig` | Optional CoreDNS configuration outside server blocks | `{}` |
| `env` | Optional array of environment variables | `[]` |
| `initContainers` | Optional array of init containers | `[]` |
| `customLabels` | Optional labels for Deployment, Pod, Service, ConfigMap | `{}` |
| `customAnnotations` | Optional annotations for Deployment, Pod, Service, ConfigMap | `{}` |
| `rollingUpdate.maxUnavailable` | Maximum number of unavailable replicas during rolling update | `1` |
| `rollingUpdate.maxSurge` | Maximum number of pods created above desired number | `25%` |
| `podDisruptionBudget` | Optional PodDisruptionBudget | `{}` |
| `podAnnotations` | Optional Pod only annotations | `{}` |
| `podLabels` | Optional Pod only labels | `{}` |
| `deployment.enabled` | Enable the main deployment | `true` |
| `deployment.skipConfig` | Skip ConfigMap creation (use external config) | `false` |
| `deployment.name` | Name of the deployment | `""` |
| `deployment.annotations` | Annotations to add to the deployment | `{}` |
| `deployment.dnsPolicy` | DNS policy for the pod (`Default`, `ClusterFirst`, `ClusterFirstWithHostNet`, `None`) | `Default` |
| `deployment.dnsConfig` | Custom DNS configuration (required when dnsPolicy is `None`) | `{}` |
| `terminationGracePeriodSeconds` | Duration in seconds the pod needs to terminate gracefully | `30` |
| `hpa.enabled` | Enable HorizontalPodAutoscaler | `false` |
| `hpa.minReplicas` | HPA minimum number of replicas | `1` |
| `hpa.maxReplicas` | HPA maximum number of replicas | `2` |
| `hpa.metrics` | Metrics definitions used by HPA to scale | `[]` |
| `hpa.behavior` | HPA scaling behavior | |

The `k8s_gateway` entry in `servers[].plugins[]` must keep the exact name
`k8s_gateway`. Renaming the plugin entry prevents CoreDNS from loading this
plugin. Gateway API routes support both Gateway and ListenerSet objects in
`spec.parentRefs`; ListenerSets are resolved through their parent Gateway.

## Caveats

The chart will automatically determine which protocols to listen on based on the protocols you define in your zones. This means you could potentially use both TCP and UDP on a single port. Some cloud environments (e.g. GCE, Azure Container Service) cannot create external loadbalancers with both TCP and UDP protocols. When deploying with `serviceType=LoadBalancer` on such environments, make sure you do not attempt to use both protocols at the same time.

To prefer IP addresses from Service and Ingress load balancer status, add the
`loadBalancerAddressPreference ip` directive to the `k8s_gateway` plugin
`configBlock`. The `ip` setting falls back to the hostname when no valid IP is
available. The default `hostname` behavior can be left unchanged by omitting
the directive:

```yaml
servers:
  - zones:
      - zone: example.com
    plugins:
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          loadBalancerAddressPreference ip
          resources Service Ingress
```

## Examples

### Basic Usage

Set the domain you want to delegate and configure the `k8s_gateway` plugin with the resources to watch:

```yaml
servers:
  - zones:
    - zone: example.com
    port: 53
    plugins:
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          apex dns.example.com
          ttl 30
          resources Service Ingress
      - name: forward
        parameters: . /etc/resolv.conf
      - name: cache
        parameters: 30
```

### DNS over TLS (DoT)

To enable DNS over TLS, set the zone scheme to `tls://`, configure the `tls` plugin, and mount the certificates:

```yaml
servers:
  - zones:
    - zone: example.com
      scheme: tls://
    port: 53
    plugins:
      - name: tls
        parameters: /etc/coredns/tls/tls.crt /etc/coredns/tls/tls.key
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          apex dns.example.com
          ttl 30
          resources Service Ingress
      - name: forward
        parameters: . /etc/resolv.conf
      - name: cache
        parameters: 30

extraVolumes:
  - name: tls-certs
    secret:
      secretName: coredns-tls-secret

extraVolumeMounts:
  - name: tls-certs
    mountPath: /etc/coredns/tls
    readOnly: true
```

For more information, see the [CoreDNS TLS plugin documentation](https://coredns.io/plugins/tls/).

### DNS over HTTPS (DoH)

To enable DNS over HTTPS, set the zone scheme to `https://` and configure the `grpc` plugin with the TLS certificates (DoH relies on TLS underneath):

```yaml
servers:
  - zones:
    - zone: example.com
      scheme: https://
    port: 443
    plugins:
      - name: tls
        parameters: /etc/coredns/tls/tls.crt /etc/coredns/tls/tls.key
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          apex dns.example.com
          ttl 30
          resources Service Ingress
      - name: forward
        parameters: . /etc/resolv.conf
      - name: cache
        parameters: 30

extraVolumes:
  - name: tls-certs
    secret:
      secretName: coredns-tls-secret

extraVolumeMounts:
  - name: tls-certs
    mountPath: /etc/coredns/tls
    readOnly: true
```

For more information, see the [CoreDNS DNS-over-HTTPS RFC](https://datatracker.ietf.org/doc/html/rfc8484).

### Running Multiple Protocols in Parallel

You can serve plain DNS, DoT, and DoH simultaneously by defining multiple server blocks with different schemes and ports:

```yaml
servers:
  # Plain DNS on UDP port 53
  - zones:
    - zone: example.com
    port: 53
    plugins:
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          apex dns.example.com
          ttl 30
          resources Service Ingress
      - name: forward
        parameters: . /etc/resolv.conf
      - name: cache
        parameters: 30

  # DNS over TLS on port 853
  - zones:
    - zone: example.com
      scheme: tls://
    port: 853
    plugins:
      - name: tls
        parameters: /etc/coredns/tls/tls.crt /etc/coredns/tls/tls.key
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          apex dns.example.com
          ttl 30
          resources Service Ingress
      - name: forward
        parameters: . /etc/resolv.conf
      - name: cache
        parameters: 30

  # DNS over HTTPS on port 443
  - zones:
    - zone: example.com
      scheme: https://
    port: 443
    plugins:
      - name: tls
        parameters: /etc/coredns/tls/tls.crt /etc/coredns/tls/tls.key
      - name: k8s_gateway
        parameters: example.com
        configBlock: |-
          apex dns.example.com
          ttl 30
          resources Service Ingress
      - name: forward
        parameters: . /etc/resolv.conf
      - name: cache
        parameters: 30

extraVolumes:
  - name: tls-certs
    secret:
      secretName: coredns-tls-secret

extraVolumeMounts:
  - name: tls-certs
    mountPath: /etc/coredns/tls
    readOnly: true
```

> **Note**: When using multiple server blocks on different ports, the chart automatically generates the corresponding Service port entries for each protocol.
