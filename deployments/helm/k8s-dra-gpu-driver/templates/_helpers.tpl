{{/*
Expand the name of the chart.
*/}}
{{- define "k8s-dra-driver.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "k8s-dra-driver.fullname" -}}
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
{{- define "k8s-dra-driver.namespace" -}}
  {{- if .Values.namespaceOverride -}}
    {{- .Values.namespaceOverride -}}
  {{- else -}}
    {{- .Release.Namespace -}}
  {{- end -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "k8s-dra-driver.chart" -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" $name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "k8s-dra-driver.labels" -}}
helm.sh/chart: {{ include "k8s-dra-driver.chart" . }}
{{ include "k8s-dra-driver.templateLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Template labels
*/}}
{{- define "k8s-dra-driver.templateLabels" -}}
app.kubernetes.io/name: {{ include "k8s-dra-driver.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- if .Values.selectorLabelsOverride }}
{{ toYaml .Values.selectorLabelsOverride }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "k8s-dra-driver.selectorLabels" -}}
{{- if .Values.selectorLabelsOverride -}}
{{ toYaml .Values.selectorLabelsOverride }}
{{- else -}}
{{ include "k8s-dra-driver.templateLabels" . }}
{{- end }}
{{- end }}

{{/*
Full image name with tag
*/}}
{{- define "k8s-dra-driver.fullimage" -}}
{{- $tag := printf "v%s" .Chart.AppVersion }}
{{- .Values.image.repository -}}:{{- .Values.image.tag | default $tag -}}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "k8s-dra-driver.serviceAccountName" -}}
{{- $name := printf "%s-service-account" (include "k8s-dra-driver.fullname" .) }}
{{- if .Values.serviceAccount.create }}
{{- default $name .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Check for the existence of an element in a list
*/}}
{{- define "k8s-dra-driver.listHas" -}}
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
{{- define "k8s-dra-driver.filterList" -}}
  {{- $listToFilter := index . 0 }}
  {{- $validValues := index . 1 }}

  {{- $result := list -}}
  {{- range $validValues}}
    {{- if include "k8s-dra-driver.listHas" (list $listToFilter .) }}
      {{- $result = append $result . }}
    {{- end }}
  {{- end }}
  {{- $result -}}
{{- end -}}
