module github.com/openshift-pipelines/pipelines-as-code

go 1.26.5

require (
	codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3 v3.0.0
	github.com/AlecAivazis/survey/v2 v2.3.7
	github.com/bradleyfalzon/ghinstallation/v2 v2.19.0
	github.com/chzyer/readline v1.5.1
	github.com/cloudevents/sdk-go/v2 v2.16.2
	github.com/fvbommel/sortorder v1.1.0
	github.com/gobwas/glob v0.2.3
	github.com/google/cel-go v0.31.0
	github.com/google/go-cmp v0.7.0
	github.com/google/go-github/scrape v0.0.0-20260808022349-848675fe0454
	github.com/hako/durafmt v0.0.0-20210608085754-5c1018a4e16b
	github.com/hashicorp/go-retryablehttp v0.7.8
	github.com/jenkins-x/go-scm v1.15.36
	github.com/jonboulle/clockwork v0.5.0
	github.com/juju/ansiterm v1.0.0
	github.com/ktrysmt/go-bitbucket v0.10.0
	github.com/mattn/go-colorable v0.1.15
	github.com/mattn/go-isatty v0.0.24
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d
	github.com/mitchellh/mapstructure v1.5.0
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.10.2
	github.com/tektoncd/pipeline v1.15.0
	gitlab.com/gitlab-org/api/client-go v1.46.0
	// otel is pinned via the replace block below, see the comment there.
	go.opentelemetry.io/otel v1.45.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.44.0
	go.opentelemetry.io/otel/metric v1.45.0
	go.opentelemetry.io/otel/sdk v1.45.0
	go.opentelemetry.io/otel/sdk/metric v1.44.0
	go.opentelemetry.io/otel/trace v1.45.0
	go.uber.org/zap v1.28.0
	golang.org/x/exp v0.0.0-20260811152304-ee035b5b010f
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.22.0
	golang.org/x/text v0.41.0
	gopkg.in/yaml.v2 v2.4.0
	gotest.tools/v3 v3.5.2
	k8s.io/api v0.36.3
	k8s.io/apimachinery v0.36.3
	k8s.io/client-go v0.36.3
	k8s.io/utils v0.0.0-20260707023825-cf1189d6abe3
	knative.dev/eventing v0.50.0
	knative.dev/pkg v0.0.0-20260727151759-521cb33b33dd
	sigs.k8s.io/yaml v1.6.0
)

require (
	github.com/google/go-github/v88 v88.0.0
	github.com/google/go-github/v90 v90.0.0
)

require (
	cel.dev/expr v0.25.2 // indirect
	github.com/42wim/httpsig v1.2.4 // indirect
	github.com/antlr/antlr4/runtime/Go/antlr v1.4.10 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cert-manager/cert-manager v1.21.1 // indirect
	github.com/cloudevents/sdk-go/observability/opentelemetry/v2 v2.16.2 // indirect
	github.com/cloudevents/sdk-go/sql/v2 v2.16.2 // indirect
	github.com/coreos/go-oidc/v3 v3.20.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/go-jose/go-jose/v3 v3.0.5 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/errors v0.22.8 // indirect
	github.com/go-openapi/strfmt v0.27.0 // indirect
	github.com/go-openapi/swag/cmdutils v0.28.0 // indirect
	github.com/go-openapi/swag/conv v0.28.0 // indirect
	github.com/go-openapi/swag/fileutils v0.28.0 // indirect
	github.com/go-openapi/swag/jsonutils v0.28.0 // indirect
	github.com/go-openapi/swag/loading v0.28.0 // indirect
	github.com/go-openapi/swag/mangling v0.28.0 // indirect
	github.com/go-openapi/swag/netutils v0.28.0 // indirect
	github.com/go-openapi/swag/pools v0.28.0 // indirect
	github.com/go-openapi/swag/stringutils v0.28.0 // indirect
	github.com/go-openapi/swag/typeutils v0.28.0 // indirect
	github.com/go-openapi/swag/yamlutils v0.28.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/oklog/ulid/v2 v2.1.2 // indirect
	github.com/prometheus/otlptranslator v1.0.0 // indirect
	github.com/rickb777/plural v1.4.11 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.70.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/runtime v0.69.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/prometheus v0.66.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.45.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.13.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260721132016-d427ff9ee9ad // indirect
	sigs.k8s.io/gateway-api v1.6.1 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.2 // indirect
)

require (
	github.com/PuerkitoBio/goquery v1.12.0 // indirect
	github.com/andybalholm/cascadia v1.3.4 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/emicklei/go-restful/v3 v3.13.0 // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/go-fed/httpsig v1.1.1-0.20201223112313-55836744818e // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-openapi/jsonpointer v1.0.0 // indirect
	github.com/go-openapi/jsonreference v1.0.0 // indirect
	github.com/go-openapi/swag v0.28.0 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.2
	github.com/google/gnostic-models v0.7.1 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-version v1.9.0 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/kelseyhightower/envconfig v1.4.0 // indirect
	github.com/lunixbochs/vtclean v1.0.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.2
	github.com/prometheus/common v0.70.1
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/rickb777/date v1.22.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/xlzd/gotp v0.1.0 // indirect
	go.uber.org/automaxprocs v1.6.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0
	golang.org/x/time v0.15.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.5.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260810153831-ec0a7760b754
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260810153831-ec0a7760b754 // indirect
	google.golang.org/grpc v1.83.0 // indirect
	google.golang.org/protobuf v1.36.12
	gopkg.in/inf.v0 v0.9.1 // indirect
	k8s.io/apiextensions-apiserver v0.36.3 // indirect
	k8s.io/klog/v2 v2.140.0
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
)

// otel modules are pinned to v1.44.0 (and contrib to v0.69.0, the prometheus
// exporter to v0.66.0): knative.dev/pkg observability/semconv is hardcoded to
// semconv 1.41.0 while otel SDK >= 1.45.0 defaults to 1.43.0, making
// resource.Merge fail and knative panic at startup with "conflicting Schema
// URL". Using replace (not just require) so a manual `go get -u` can't
// silently re-bump these. Remove once knative.dev/pkg catches up.
replace (
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp => go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0
	go.opentelemetry.io/contrib/instrumentation/runtime => go.opentelemetry.io/contrib/instrumentation/runtime v0.69.0
	go.opentelemetry.io/otel => go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc => go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp => go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace => go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc => go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp => go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.44.0
	go.opentelemetry.io/otel/exporters/prometheus => go.opentelemetry.io/otel/exporters/prometheus v0.66.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace => go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.44.0
	go.opentelemetry.io/otel/metric => go.opentelemetry.io/otel/metric v1.44.0
	go.opentelemetry.io/otel/sdk => go.opentelemetry.io/otel/sdk v1.44.0
	go.opentelemetry.io/otel/sdk/metric => go.opentelemetry.io/otel/sdk/metric v1.44.0
	go.opentelemetry.io/otel/trace => go.opentelemetry.io/otel/trace v1.44.0
)
