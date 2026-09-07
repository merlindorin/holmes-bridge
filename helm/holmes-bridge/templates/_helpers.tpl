{{- define "holmes-bridge.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "holmes-bridge.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "holmes-bridge.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "holmes-bridge.labels" -}}
helm.sh/chart: {{ include "holmes-bridge.chart" . }}
{{ include "holmes-bridge.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "holmes-bridge.selectorLabels" -}}
app.kubernetes.io/name: {{ include "holmes-bridge.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "holmes-bridge.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "holmes-bridge.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
The Secret holding credentials: one we create, or one you manage.

Note the subchart reads this by a fixed name (holmes-bridge-credentials in
values.yaml), so overriding it here means overriding it there too.
*/}}
{{- define "holmes-bridge.secretName" -}}
{{- if .Values.secrets.existingSecret }}
{{- .Values.secrets.existingSecret }}
{{- else }}
{{- include "holmes-bridge.fullname" . }}-credentials
{{- end }}
{{- end }}

{{/*
Where the bridge finds HolmesGPT.

The upstream chart names its Service "<release>-holmes" and exposes port 80,
targeting 5050 in the container — not "holmes:5050", which is the shape people
reach for and which silently never connects.
*/}}
{{- define "holmes-bridge.holmesUrl" -}}
{{- if .Values.holmesUrl }}
{{- .Values.holmesUrl }}
{{- else if .Values.holmes.enabled }}
{{- printf "http://%s-holmes.%s.svc.cluster.local" .Release.Name .Release.Namespace }}
{{- else }}
{{- fail "set holmesUrl, or holmes.enabled=true to deploy HolmesGPT alongside the bridge" }}
{{- end }}
{{- end }}

{{/*
The subcommand for the selected pipeline. They are different flows, so this is
a choice rather than a toggle.
*/}}
{{- define "holmes-bridge.args" -}}
{{- if eq .Values.pipeline "incidentio" }}
- incidentio
- serve
{{- range .Values.incidentio.triggers }}
- --trigger={{ . }}
{{- end }}
{{- else if eq .Values.pipeline "alertmanager" }}
- ntfy
- serve
{{- else }}
{{- fail (printf "pipeline must be \"incidentio\" or \"alertmanager\", got %q" .Values.pipeline) }}
{{- end }}
{{- if .Values.expose.enabled }}
- --expose
- --expose-peer={{ .Values.expose.peer }}
{{- with .Values.expose.adminUrl }}
- --expose-admin-url={{ . }}
{{- end }}
{{- end }}
{{- end }}
