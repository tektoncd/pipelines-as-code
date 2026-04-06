module github.com/openshift-pipelines/pipelines-as-code

go 1.24.0

toolchain go1.24.2

require (
	code.gitea.io/gitea v1.23.7
	code.gitea.io/sdk/gitea v0.21.0
	github.com/AlecAivazis/survey/v2 v2.3.7
	github.com/bradleyfalzon/ghinstallation/v2 v2.15.0
	github.com/cloudevents/sdk-go/v2 v2.16.0
	github.com/fvbommel/sortorder v1.1.0
	github.com/gobwas/glob v0.2.3
	github.com/google/cel-go v0.25.0
	github.com/google/go-cmp v0.7.0
	github.com/google/go-github/scrape v0.0.0-20250430122735-c9e1ad0d3b65
	github.com/google/go-github/v71 v71.0.0
	github.com/hako/durafmt v0.0.0-20210608085754-5c1018a4e16b
	github.com/jenkins-x/go-scm v1.15.10
	github.com/jonboulle/clockwork v0.5.0
	github.com/juju/ansiterm v1.0.0
	github.com/ktrysmt/go-bitbucket v0.9.85
	github.com/mattn/go-colorable v0.1.14
	github.com/mattn/go-isatty v0.0.20
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d
	github.com/mitchellh/mapstructure v1.5.0
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.9.1
	github.com/stretchr/testify v1.10.0
	github.com/tektoncd/pipeline v1.0.1
	gitlab.com/gitlab-org/api/client-go v0.128.0
	go.opencensus.io v0.24.0
	go.uber.org/zap v1.27.0
	golang.org/x/exp v0.0.0-20250408133849-7e4ce0ab07d0
	golang.org/x/oauth2 v0.34.0
	golang.org/x/sync v0.19.0
	golang.org/x/text v0.32.0
	gopkg.in/yaml.v2 v2.4.0
	gotest.tools/v3 v3.5.2
	k8s.io/api v0.32.4
	k8s.io/apimachinery v0.33.3
	k8s.io/client-go v0.32.4
	k8s.io/utils v0.0.0-20250502105355-0f33e8f1c979
	knative.dev/eventing v0.45.0
	knative.dev/pkg v0.0.0-20250424013628-d5e74d29daa3
	sigs.k8s.io/yaml v1.4.0
)

require (
	cel.dev/expr v0.25.1 // indirect
	github.com/42wim/httpsig v1.2.3 // indirect
	github.com/antlr/antlr4/runtime/Go/antlr v1.4.10 // indirect
	github.com/cert-manager/cert-manager v1.17.2 // indirect
	github.com/cloudevents/sdk-go/sql/v2 v2.0.0-20240712172937-3ce6b2f1f011 // indirect
	github.com/coreos/go-oidc/v3 v3.14.1 // indirect
	github.com/fxamacker/cbor/v2 v2.8.0 // indirect
	github.com/go-jose/go-jose/v3 v3.0.4 // indirect
	github.com/go-jose/go-jose/v4 v4.1.3 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/rickb777/plural v1.4.3 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	sigs.k8s.io/gateway-api v1.1.0 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
)

require (
	contrib.go.opencensus.io/exporter/ocagent v0.7.1-0.20200907061046-05415f1de66d // indirect
	contrib.go.opencensus.io/exporter/prometheus v0.4.2 // indirect
	contrib.go.opencensus.io/exporter/zipkin v0.1.2 // indirect
	github.com/PuerkitoBio/goquery v1.10.3 // indirect
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blang/semver/v4 v4.0.0 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/census-instrumentation/opencensus-proto v0.4.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudevents/sdk-go/observability/opencensus/v2 v2.15.2 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/emicklei/go-restful/v3 v3.12.2 // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/go-fed/httpsig v1.1.1-0.20201223112313-55836744818e // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-openapi/jsonpointer v0.21.1 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.1 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v4 v4.5.2
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.6.9 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.26.3 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.7 // indirect
	github.com/hashicorp/go-version v1.7.0 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/kelseyhightower/envconfig v1.4.0 // indirect
	github.com/lunixbochs/vtclean v1.0.0 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/openzipkin/zipkin-go v0.4.3 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_golang v1.22.0 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.63.0 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/prometheus/statsd_exporter v0.28.0 // indirect
	github.com/rickb777/date v1.21.1 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	github.com/stoewer/go-strcase v1.3.0 // indirect
	github.com/xlzd/gotp v0.1.0 // indirect
	go.uber.org/automaxprocs v1.6.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/term v0.38.0 // indirect
	golang.org/x/time v0.11.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.5.0 // indirect
	google.golang.org/api v0.231.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/grpc v1.79.3 // indirect
	google.golang.org/protobuf v1.36.10
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiextensions-apiserver v0.32.4 // indirect
	k8s.io/klog/v2 v2.130.1
	k8s.io/kube-openapi v0.0.0-20250318190949-c8a335a9a2ff // indirect
	sigs.k8s.io/json v0.0.0-20241014173422-cfa47c3a1cc8 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.7.0 // indirect
)

replace (
	github.com/go-jose/go-jose/v4 => github.com/go-jose/go-jose/v4 v4.0.5
	k8s.io/api => k8s.io/api v0.32.4
	k8s.io/apimachinery => k8s.io/apimachinery v0.32.4
	sigs.k8s.io/gateway-api => sigs.k8s.io/gateway-api v1.0.0
)
