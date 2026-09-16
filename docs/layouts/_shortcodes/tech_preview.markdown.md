> [!WARNING]
>
{{- if .Get 0 }}
> **{{ .Get 0 }}** is a **Technology Preview** feature only.
{{- else }}
> This is a **Technology Preview** feature only.
{{- end }}
> Technology Preview features are not currently supported and might not be functionally complete. We do not recommend using them in production. These features provide early access to upcoming Pipelines-as-Code features, enabling you to test functionality and provide feedback during the development process.
