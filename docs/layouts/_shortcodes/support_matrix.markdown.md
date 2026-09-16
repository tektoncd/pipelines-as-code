| Git Provider | Supported |
| --- | --- |
| GitHub App | {{ if eq (.Get "github_app") "true" }}✅{{ else }}❌{{ end }} |
| GitHub Webhook | {{ if eq (.Get "github_webhook") "true" }}✅{{ else }}❌{{ end }} |
| Forgejo | {{ if eq (.Get "forgejo") "true" }}✅{{ else }}❌{{ end }} |
| GitLab | {{ if eq (.Get "gitlab") "true" }}✅{{ else }}❌{{ end }} |
| Bitbucket Cloud | {{ if eq (.Get "bitbucket_cloud") "true" }}✅{{ else }}❌{{ end }} |
| Bitbucket Data Center | {{ if eq (.Get "bitbucket_datacenter") "true" }}✅{{ else }}❌{{ end }} |
