{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "k8s-gateway.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
*/}}
{{- define "k8s-gateway.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Common labels
*/}}
{{- define "k8s-gateway.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
app.kubernetes.io/instance: {{ .Release.Name | quote }}
helm.sh/chart: "{{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}"
app.kubernetes.io/name: {{ template "k8s-gateway.name" . }}
{{- end -}}

{{/*
Allow k8s-app label to be overridden
*/}}
{{- define "k8s-gateway.k8sapplabel" -}}
{{- default .Chart.Name .Values.k8sAppLabelOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Generate the list of ports automatically from the server definitions
*/}}
{{- define "k8s-gateway.servicePorts" -}}
    {{/* Set ports to be an empty dict */}}
    {{- $ports := dict -}}
    {{/* Iterate through each of the server blocks */}}
    {{- range .Values.servers -}}
        {{/* Capture port to avoid scoping awkwardness */}}
        {{- $port := toString .port -}}
        {{- $serviceport := default .port .servicePort -}}

        {{/* If none of the server blocks has mentioned this port yet take note of it */}}
        {{- if not (hasKey $ports $port) -}}
            {{- $ports := set $ports $port (dict "istcp" false "isudp" false "serviceport" $serviceport) -}}
        {{- end -}}
        {{/* Retrieve the inner dict that holds the protocols for a given port */}}
        {{- $innerdict := index $ports $port -}}

        {{/*
        Look at each of the zones and check which protocol they serve
        At the moment the following are supported by k8s-gateway:
        UDP: dns://
        TCP: tls://, grpc://, https://
        */}}
        {{- range .zones -}}
            {{- if has (default "" .scheme) (list "dns://" "") -}}
                {{/* Optionally enable tcp for this service as well */}}
                {{- if eq (default false .use_tcp) true }}
                    {{- $innerdict := set $innerdict "istcp" true -}}
                {{- end }}
                {{- $innerdict := set $innerdict "isudp" true -}}
            {{- end -}}

            {{- if has (default "" .scheme) (list "tls://" "grpc://" "https://") -}}
                {{- $innerdict := set $innerdict "istcp" true -}}
            {{- end -}}
        {{- end -}}

        {{/* If none of the zones specify scheme, default to dns:// udp */}}
        {{- if and (not (index $innerdict "istcp")) (not (index $innerdict "isudp")) -}}
            {{- $innerdict := set $innerdict "isudp" true -}}
        {{- end -}}

        {{- if .nodePort -}}
            {{- $innerdict := set $innerdict "nodePort" .nodePort -}}
        {{- end -}}

        {{/* Write the dict back into the outer dict */}}
        {{- $ports := set $ports $port $innerdict -}}
    {{- end -}}

    {{/* Write out the ports according to the info collected above */}}
    {{- range $port, $innerdict := $ports -}}
        {{- $portList := list -}}
        {{- if index $innerdict "isudp" -}}
            {{- $portList = append $portList (dict "port" (get $innerdict "serviceport") "protocol" "UDP" "name" (printf "udp-%s" $port) "targetPort" ($port | int)) -}}
        {{- end -}}
        {{- if index $innerdict "istcp" -}}
            {{- $portList = append $portList (dict "port" (get $innerdict "serviceport") "protocol" "TCP" "name" (printf "tcp-%s" $port) "targetPort" ($port | int)) -}}
        {{- end -}}

        {{- range $portDict := $portList -}}
            {{- if index $innerdict "nodePort" -}}
                {{- $portDict := set $portDict "nodePort" (get $innerdict "nodePort" | int) -}}
            {{- end -}}

            {{- printf "- %s\n" (toJson $portDict) -}}
        {{- end -}}
    {{- end -}}
{{- end -}}

{{/*
Generate the list of ports automatically from the server definitions
*/}}
{{- define "k8s-gateway.containerPorts" -}}
    {{/* Set ports to be an empty dict */}}
    {{- $ports := dict -}}
    {{/* Iterate through each of the server blocks */}}
    {{- range .Values.servers -}}
        {{/* Capture port to avoid scoping awkwardness */}}
        {{- $port := toString .port -}}

        {{/* If none of the server blocks has mentioned this port yet take note of it */}}
        {{- if not (hasKey $ports $port) -}}
            {{- $ports := set $ports $port (dict "istcp" false "isudp" false) -}}
        {{- end -}}
        {{/* Retrieve the inner dict that holds the protocols for a given port */}}
        {{- $innerdict := index $ports $port -}}

        {{/*
        Look at each of the zones and check which protocol they serve
        At the moment the following are supported by k8s-gateway:
        UDP: dns://
        TCP: tls://, grpc://, https://
        */}}
        {{- range .zones -}}
            {{- if has (default "" .scheme) (list "dns://" "") -}}
                {{/* Optionally enable tcp for this service as well */}}
                {{- if eq (default false .use_tcp) true }}
                    {{- $innerdict := set $innerdict "istcp" true -}}
                {{- end }}
                {{- $innerdict := set $innerdict "isudp" true -}}
            {{- end -}}

            {{- if has (default "" .scheme) (list "tls://" "grpc://" "https://") -}}
                {{- $innerdict := set $innerdict "istcp" true -}}
            {{- end -}}
        {{- end -}}

        {{/* If none of the zones specify scheme, default to dns:// udp */}}
        {{- if and (not (index $innerdict "istcp")) (not (index $innerdict "isudp")) -}}
            {{- $innerdict := set $innerdict "isudp" true -}}
        {{- end -}}

        {{- if .hostPort -}}
            {{- $innerdict := set $innerdict "hostPort" .hostPort -}}
        {{- end -}}

        {{/* Write the dict back into the outer dict */}}
        {{- $ports := set $ports $port $innerdict -}}

        {{/* Fetch port from the configuration if the prometheus section exists */}}
        {{- range .plugins -}}
            {{- if eq .name "prometheus" -}}
                {{- $prometheus_addr := toString .parameters -}}
                {{- $prometheus_addr_list := regexSplit ":" $prometheus_addr -1 -}}
                {{- $prometheus_port := index $prometheus_addr_list 1 -}}
                {{- $ports := set $ports $prometheus_port (dict "istcp" true "isudp" false) -}}
            {{- end -}}
        {{- end -}}
    {{- end -}}

    {{/* Write out the ports according to the info collected above */}}
    {{- range $port, $innerdict := $ports -}}
        {{- $portList := list -}}
        {{- if index $innerdict "isudp" -}}
            {{- $portList = append $portList (dict "containerPort" ($port | int) "protocol" "UDP" "name" (printf "udp-%s" $port)) -}}
        {{- end -}}
        {{- if index $innerdict "istcp" -}}
            {{- $portList = append $portList (dict "containerPort" ($port | int) "protocol" "TCP" "name" (printf "tcp-%s" $port)) -}}
        {{- end -}}

        {{- range $portDict := $portList -}}
            {{- if index $innerdict "hostPort" -}}
                {{- $portDict := set $portDict "hostPort" (get $innerdict "hostPort" | int) -}}
            {{- end -}}

            {{- printf "- %s\n" (toJson $portDict) -}}
        {{- end -}}
    {{- end -}}
{{- end -}}

{{/*
Create the name of the service account to use
*/}}
{{- define "k8s-gateway.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
    {{ default (include "k8s-gateway.fullname" .) .Values.serviceAccount.name }}
{{- else -}}
    {{ default "default" .Values.serviceAccount.name }}
{{- end -}}
{{- end -}}

{{/*
Create the name of the service account to use
*/}}
{{- define "k8s-gateway.clusterRoleName" -}}
{{- if and .Values.clusterRole .Values.clusterRole.nameOverride -}}
    {{ .Values.clusterRole.nameOverride }}
{{- else -}}
    {{ template "k8s-gateway.fullname" . }}
{{- end -}}
{{- end -}}


{{/*
Returns "true" if any plugin declares the given resource name
in either its in configBlock.
Usage: include "k8s-gateway.hasResource" (list . "Ingress")
*/}}
{{- define "k8s-gateway.hasResource" -}}
  {{- $root := index . 0 -}}
  {{- $resource := index . 1 -}}
  {{- $enabled := "false" -}}
  {{- range $root.Values.servers -}}
    {{- range .plugins -}}
      {{- $config := lower (toString (default "" .configBlock)) -}}
      {{- $needle := lower $resource -}}
      {{- if (contains $needle $config) -}}
        {{- $enabled = "true" -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
  {{- $enabled -}}
{{- end -}}

{{/*
Service — matches "service" in configBlock
*/}}
{{- define "k8s-gateway.service" -}}
  {{- include "k8s-gateway.hasResource" (list . "Service") -}}
{{- end -}}

{{/*
Ingress — matches "ingress" in configBlock
*/}}
{{- define "k8s-gateway.ingress" -}}
  {{- include "k8s-gateway.hasResource" (list . "Ingress") -}}
{{- end -}}

{{/*
DNSEndpoint — matches "dnsendpoint" in configBlock
*/}}
{{- define "k8s-gateway.dnsEndpoint" -}}
  {{- include "k8s-gateway.hasResource" (list . "DNSEndpoint") -}}
{{- end -}}

{{/*
Gateway API — any of HTTPRoute, TLSRoute, GRPCRoute trigger this
*/}}
{{- define "k8s-gateway.gatewayAPI" -}}
  {{- $root := . -}}
  {{- $enabled := "false" -}}
  {{- range list "HTTPRoute" "TLSRoute" "GRPCRoute" -}}
    {{- if eq (include "k8s-gateway.hasResource" (list $root .)) "true" -}}
      {{- $enabled = "true" -}}
    {{- end -}}
  {{- end -}}
  {{- $enabled -}}
{{- end -}}

{{/*
Node — matches "node" in configBlock
*/}}
{{- define "k8s-gateway.node" -}}
  {{- include "k8s-gateway.hasResource" (list . "Node") -}}
{{- end -}}