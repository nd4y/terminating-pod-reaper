{{/* Базовое имя чарта */}}
{{- define "reaper.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Полное имя релиза */}}
{{- define "reaper.fullname" -}}
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

{{/* Общие метки */}}
{{- define "reaper.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "reaper.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Селектор-метки */}}
{{- define "reaper.selectorLabels" -}}
app.kubernetes.io/name: {{ include "reaper.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Имя ServiceAccount */}}
{{- define "reaper.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "reaper.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* Тег образа */}}
{{- define "reaper.imageTag" -}}
{{- default .Chart.AppVersion .Values.image.tag -}}
{{- end -}}

{{/* Кластерный режим (leader election): явно включён ИЛИ реплик больше одной. Возвращает "true"/"false". */}}
{{- define "reaper.leaderElect" -}}
{{- if or .Values.leaderElection.enabled (gt (int .Values.replicaCount) 1) -}}true{{- else -}}false{{- end -}}
{{- end -}}
