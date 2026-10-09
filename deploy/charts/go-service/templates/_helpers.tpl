{{- define "go-service.name" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "go-service.labels" -}}
app.kubernetes.io/name: {{ include "go-service.name" . }}
app.kubernetes.io/part-of: datastack
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{- define "go-service.selectorLabels" -}}
app.kubernetes.io/name: {{ include "go-service.name" . }}
{{- end -}}

{{/*
Database connection settings shared by the migrate initContainer and the
serve container. Credentials come from CNPG's secret through PGUSER and
PGPASSWORD, which pgx reads automatically. That keeps generated passwords
out of the URL, so they never need escaping.
*/}}
{{- define "go-service.databaseEnv" -}}
- name: PGUSER
  valueFrom:
    secretKeyRef:
      name: {{ .Values.postgres.credentialsSecret }}
      key: username
- name: PGPASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .Values.postgres.credentialsSecret }}
      key: password
- name: DATABASE_URL
  value: "postgres://{{ .Values.postgres.cluster }}-rw:5432/{{ .Values.postgres.database }}?sslmode={{ .Values.postgres.sslmode }}"
{{- end -}}

{{- define "go-service.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
{{- end -}}
