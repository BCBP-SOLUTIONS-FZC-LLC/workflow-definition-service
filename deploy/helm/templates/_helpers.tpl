{{/*
Expand the name of the chart.
*/}}
{{- define "workflow-definition-service.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "workflow-definition-service.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- printf "%s" $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Common labels applied to every resource.
*/}}
{{- define "workflow-definition-service.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "workflow-definition-service.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels — used by Deployment, Service, HPA, and PDB.
*/}}
{{- define "workflow-definition-service.selectorLabels" -}}
app.kubernetes.io/name: {{ include "workflow-definition-service.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Image tag: prefer .Values.image.tag; fall back to chart appVersion.
*/}}
{{- define "workflow-definition-service.imageTag" -}}
{{- .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
Image reference: repository:tag, pinned by digest when image.digest is set.
*/}}
{{- define "workflow-definition-service.image" -}}
{{ .Values.image.repository }}:{{ include "workflow-definition-service.imageTag" . }}{{ with .Values.image.digest }}@{{ . }}{{ end }}
{{- end }}

{{- define "workflow-definition-service.purgeAlertWindow" -}}
{{- $interval := .Values.env.TENANT_PURGE_INTERVAL | toString -}}
{{- if not (regexMatch "^[0-9]+h$" $interval) -}}
{{- fail "env.TENANT_PURGE_INTERVAL must be whole hours (for example 24h), so the purge alert window can follow it" -}}
{{- end -}}
{{- printf "%dh" (add (trimSuffix "h" $interval | atoi) 2) -}}
{{- end -}}

{{/*
With the policy off, the NetworkPolicy alone fronts the internal routes: refused in staging and prod unless accepted.
*/}}
{{- define "workflow-definition-service.validateAuthorizationPolicy" -}}
{{- if and (not .Values.authorizationPolicy.enabled) (has .Values.env.APP_ENV (list "prod" "staging")) (not .Values.authorizationPolicy.allowDisabled) -}}
{{- fail (printf "authorizationPolicy.enabled is false while env.APP_ENV is %s: only the NetworkPolicy would stand in front of the internal routes. Enable the policy, or set authorizationPolicy.allowDisabled=true to accept that" .Values.env.APP_ENV) -}}
{{- end -}}
{{- end }}
