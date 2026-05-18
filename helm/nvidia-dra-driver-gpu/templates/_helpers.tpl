{{/*
Expand the name of the chart.
*/}}
{{- define "nvidia-dra-driver-gpu.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "nvidia-dra-driver-gpu.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Allow the release namespace to be overridden for multi-namespace deployments in combined charts
*/}}
{{- define "nvidia-dra-driver-gpu.namespace" -}}
  {{- if .Values.namespaceOverride -}}
    {{- .Values.namespaceOverride -}}
  {{- else -}}
    {{- .Release.Namespace -}}
  {{- end -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "nvidia-dra-driver-gpu.chart" -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" $name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Standard labels: documented at
https://helm.sh/docs/chart_best_practices/labels/
Apply this to all high-level objects (Deployment, DaemonSet, ...).
Pod template labels are included here to deliver name+instance.
*/}}
{{- define "nvidia-dra-driver-gpu.labels" -}}
helm.sh/chart: {{ include "nvidia-dra-driver-gpu.chart" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{ include "nvidia-dra-driver-gpu.templateLabels" . }}
{{- end }}

{{/*
Apply this to all pod templates (a smaller set of labels compared to
the set of standard labels above, to not clutter individual pods too
much). Note that these labels cannot be used to distinguish
components within this Helm chart.
*/}}
{{- define "nvidia-dra-driver-gpu.templateLabels" -}}
app.kubernetes.io/name: {{ include "nvidia-dra-driver-gpu.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Selector label: precisely filter for just the pods of the corresponding
Deployment, DaemonSet, .... That is, this label key/value pair must be
different per-component (a component name is a required argument). This
could be many labels, but we want to use just one (with a sufficiently
unique key).

TOOD: remove the override feature, or make the override work per-component.
*/}}
{{- define "nvidia-dra-driver-gpu.selectorLabels" -}}
{{- if and (hasKey . "componentName") (hasKey . "context") -}}
{{- if .context.Values.selectorLabelsOverride -}}
{{ toYaml .context.Values.selectorLabelsOverride }}
{{- else -}}
{{- $name := default .context.Chart.Name .context.Values.nameOverride -}}
{{ $name }}-component: {{ .componentName }}
{{- end }}
{{- else -}}
fail "selectorLabels: both arguments are required: context, componentName"
{{- end }}
{{- end }}

{{/*
Full image name with tag
*/}}
{{- define "nvidia-dra-driver-gpu.fullimage" -}}
{{- $tag := printf "v%s" .Chart.AppVersion }}
{{- .Values.image.repository -}}:{{- .Values.image.tag | default $tag -}}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "nvidia-dra-driver-gpu.serviceAccountName" -}}
{{- $name := printf "%s-service-account" (include "nvidia-dra-driver-gpu.fullname" .) }}
{{- if .Values.serviceAccount.create }}
{{- default $name .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Check for the existence of an element in a list
*/}}
{{- define "nvidia-dra-driver-gpu.listHas" -}}
  {{- $listToCheck := index . 0 }}
  {{- $valueToCheck := index . 1 }}

  {{- $found := "" -}}
  {{- range $listToCheck}}
    {{- if eq . $valueToCheck }}
      {{- $found = "true" -}}
    {{- end }}
  {{- end }}
  {{- $found -}}
{{- end }}

{{/*
Filter a list by a set of valid values
*/}}
{{- define "nvidia-dra-driver-gpu.filterList" -}}
  {{- $listToFilter := index . 0 }}
  {{- $validValues := index . 1 }}

  {{- $result := list -}}
  {{- range $validValues}}
    {{- if include "nvidia-dra-driver-gpu.listHas" (list $listToFilter .) }}
      {{- $result = append $result . }}
    {{- end }}
  {{- end }}
  {{- $result -}}
{{- end -}}

{{/*
Get the latest available resource.k8s.io API version
Returns the highest available version or empty string if none found
*/}}
{{- define "nvidia-dra-driver-gpu.resourceApiVersion" -}}
{{- if .Capabilities.APIVersions.Has "resource.k8s.io/v1" -}}
resource.k8s.io/v1
{{- else if .Capabilities.APIVersions.Has "resource.k8s.io/v1beta1" -}}
resource.k8s.io/v1beta1
{{- else if .Capabilities.APIVersions.Has "resource.k8s.io/v1beta2" -}}
resource.k8s.io/v1beta2
{{- else -}}
{{- end -}}
{{- end -}}
