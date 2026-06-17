---
title: Developer Resources
---

# How to get started developing for the Pipelines-as-Code project

## Please read the Code of conduct

It's important: <https://github.com/openshift-pipelines/pipelines-as-code/blob/main/code-of-conduct.md>

## Use the all in one install on kind to develop

It uses kind under docker. You start it with:

```shell
make dev
```

When it finished you will have the following installed in your kind cluster:

- Kind Cluster deployment
- Internal registry to push to from `ko`
- A ingress controller with nginx for routing.
- Tekton and Dashboard installed with a ingress route.
- Pipelines as code deployed from your repo with ko.
- Gitea service running locally so you can run your E2E tests that targets gitea (the most comprehensive ones)

By default it will try to install from
$GOPATH/src/github.com/openshift-pipelines/pipelines-as-code, if you want to
override it you can set the `PAC_DIRS` environment variable.

- It will deploy under the nip.io domain reflector, the URL will be :

  - <http://controller.paac-127-0-0-1.nip.io>
  - <http://dashboard.paac-127-0-0-1.nip.io>

- You will need to create secret yourself, if you have the [pass cli](https://www.passwordstore.org/)
  installed you can point to a folder which contains : github-application-id github-private-key webhook.secret
  As configured from your GitHub application. Configure `PAC_PASS_SECRET_FOLDER`
  environment variable to point to it.
  For example :

  ```shell
  pass insert github-app/github-application-id
  pass insert github-app/webhook.secret
  pass insert -m github-app/github-private-key
  ```

- If you need to redeploy your pac install (and only pac) you can do :

  ```shell
  ./hack/dev/kind/install.sh -p
  ```

  or

  ```shell
  make rdev
  ```

  or you can do this directly with ko :

  ```shell
  env KO_DOCKER_REPO=localhost:5000 ko apply -f ${1:-"config"} -B
  ```

- more flags: `-b` to only do the kind creation+nginx+docker image, `-r` to
  install from latest stable release (override with env variable `PAC_RELEASE`)
  instead of ko. `-c` will only do the pac configuration (ie: creation of
  secrets/ingress etc..)

- see the [install.sh](https://github.com/openshift-pipelines/pipelines-as-code/blob/main/hack/dev/kind/install.sh) -h for all flags

## Gitea

Gitea is "unofficially" supported. You just need to configure Gitea the same way
you do for other webhook methods with a token.

Here is an example of a Gitea NS/CRD/Secret (set to empty):

```yaml
---
apiVersion: v1
kind: Namespace
metadata:
  name: gitea

---
apiVersion: "pipelinesascode.tekton.dev/v1alpha1"
kind: Repository
metadata:
  name: gitea
  namespace: gitea
spec:
  url: "https://gitea.my.com/owner/repo"
  git_provider:
    user: "git"
    url: "Your gitea installation URL, i.e: https://gitea.my.com/"
    secret:
      name: "secret"
      key: token
    webhook_secret:
      name: "secret"
      key: "webhook"
---
apiVersion: v1
kind: Secret
metadata:
  name: gitea-home-chmouel
  namespace: gitea
type: Opaque
stringData:
  token: "your token has generated in gitea"
  webhook: "" # make sure it's empty when you set this up on the interface and here
```

There is some gotchas with the webhook validation secret, Pipelines-as-Code
detect a Gitea install and let the user set a empty webhook secret (by default
it's enforced).

The `install.sh` script will by default spin up a new instance of GITEA to play
with and run the Gitea E2E tests.

You will need to create a Hook URL generated from <https://hook.pipelinesascode.com/new>
into the environment variable `TEST_GITEA_SMEEURL`.

The defaults are :

- URL: <https://localhost:3000/>
- Admin Username: pac
- Admin Password: pac

The E2E tests will automatically create repo using the admin username for each tests.

## Debugging E2E

As long you have the secrets setup you should be able to run the e2e tests properly.
Gitea are the easiest to run (since they are self contained), for the other you
will need to setup some environment variables.

See the [e2e on kind
workflow](https://github.com/openshift-pipelines/pipelines-as-code/blob/8f990bf5f348f6529deaa3693257907b42287a35/.github/workflows/kind-e2e-tests.yaml#L90)
for all the variables set by provider.

By default the E2E tests cleanups after themselves if you want to keep the
PR/MR opens and the namespace where the test has been created you can set the
`TEST_NOCLEANUP` environment variable to `true`.

## Debugging controller

Create a [hook](https://hook.pipelinesascode.com) URL and point your app/webhook to it. Use
[gosmee](https://github.com/chmouel/gosmee) to forward the requests from GitHub
to your locally installed controller (this can be either run on your debugger or
inside kind).

An option of gosmee is to save the replay to a directory with `--saveDir
/tmp/save`. If go to that directory a shell script will be created to replay
the request that was sent directly to the controller without having to go through
another push.

Use [snazy](https://github.com/chmouel/snazy) to watch the logs, it support pac
by adding some context like which GitHub provider.

![snazy screenshot](/images/pac-snazy.png)

## Using the Makefile targets

Several target in the Makefile is available, if you need to run them
manually. You can list all the makefile targets with:

```shell
make help
```

For example to test and lint the go files :

```shell
make test lint-go
```

If you add a CLI command with help you will need to regenerate the golden files :

```shell
make update-golden
```

## Configuring the Pre Push Git checks

We are using several tools to verify that pipelines-as-code is up to a good
coding and documentation standard. We use pre-commit tools to ensure before you
send your PR that the commit is valid.

First you need to install pre-commit:

<https://pre-commit.com/>

It should be available as package on Fedora and Brew or install it with `pip`.

When you have it installed add the hook to your repo by doing :

```shell
pre-commit install
```

This will run several `hooks` on the files that has been changed before you
_push_ to your remote branch. If you need to skip the verification (for whatever
reason), you can do :

```shell
git push --no-verify
```

or you can disable individual hook with the `SKIP` variable:

```shell
SKIP=lint-md git push
```

If you want to manually run on everything:

```shell
make pre-commit
```

## Developing the Documentation

Documentation is important to us, most of the time new features or change of
behaviour needs to include documentation part of the Pull Request.

We use hugo, if you want to preview your change, you need to install
[hugo](https://gohugo.io) and do a :

```shell
make docs-dev
```

this will start a hugo server with live preview of the docs on :

<https://localhost:1313>

When we push the release, the docs get rebuilt by CloudFare.

By default the website <https://pipelinesascode.com> only contains the "stable"
documentation. If you want to preview the dev documentation as from `main` you
need to go to this URL:

<https://main.pipelines-as-code.pages.dev>

## Documentation when we are doing the Release Process

- See here [release-process]({{< relref "/dev/release-process.md" >}})

## How to update all dependencies in Pipelines-as-Code

### Go Modules

Unless if we that's not possible we try to update all dependencies to the
latest version as long it's compatible with the Pipeline version as shipped by
OpenShift Pipelines Operator (which should be conservative).

Every time you do a go modules update check if we can remove the `replace`
clause that pins a dependency to a specific version/commit or match the replace
to the tektoncd/pipeline version.

- Update all go modules:

  ```shell
  go get -u ./...
  make vendor
  ```

- Go to <https://github.com/google/go-github> and note the latest go version for example: v59
- Open a file that use the go-github library (ie: pkg/provider/github/detect.go) and check the old version, for example: v56

- Run this sed command:

  ```shell
  find -name '*.go'|xargs sed -i 's,github.com/google/go-github/v56,github.com/google/go-github/v59,'
  ```

- This will update everything, sometime the library ghinstallation is not
updated with the new version, so you will need to keep the old version kept in
there. For example you will get this kind of error:

  ```text
  pkg/provider/github/parse_payload.go:56:33: cannot use &github.InstallationTokenOptions{…} (value of type *"github.com/google/go-github/v59/github".InstallationTokenOptions) as *"github.com/google/go-github/v57/github".InstallationTokenOptions value in assignment
  ```

- Check that everything compiles and tests are passing with this command:

  ```shell
  make allbinaries test lint
  ```

- Some structs needs to be updated, some of them are going to fail on
  deprecated, so you will need to figure how to update them. Don't be lazy and avoid the
  update with a nolint or a pin to a dep you only delay the inevitable until
  the problem come back and hit you harder.

### Go version

- Check that the go version is updated to the latest RHEL version:

  ```shell
  docker pull golang
  docker run golang go version
  ```

- If this not the same as what we have in go.mod then you need to update the go.mod version. then you need to update for example here 1.20:

  ```shell
  go mod tidy -go=1.20
  ```

- Grep for the image go-toolset everywhere with:

  ```shell
  git grep golang:
  ```

  and change the old version to the new version

### Update the pre-commit rules

  ```shell
  pre-commit autoupdate
  ```

### Update the vale rules

  ```shell
  vale sync
  make lint-md
  ```

## Tools that are useful

Several tools are used on CI and in `pre-commit`, the non exhaustive list you
need to have on your system:

- [golangci-lint](https://github.com/golangci/golangci-lint) - For golang lint
- [yamllint](https://github.com/adrienverge/yamllint) - For YAML lint
- [shellcheck](https://www.shellcheck.net/) - For shell scripts linting
- [ruff](https://github.com/astral-sh/ruff) - Python code formatter check
- [vale](https://github.com/errata-ai/vale) - For grammar check
- [markdownlint](https://github.com/markdownlint/markdownlint) - For markdown lint
- [codespell](https://github.com/codespell-project/codespell) - For code spelling
- [gitlint](https://github.com/jorisroovers/gitlint) - For git commit messages lint
- [hugo](https://gohugo.io) - For documentation
- [ko](https://github.com/google/ko) - To rebuild and push change to kube cluster.
- [kind](https://kind.sigs.k8s.io/) - For local devs
- [snazy](https://github.com/chmouel/snazy) - To parse json logs nicely
- [pre-commit](https://pre-commit.com/) - For checking commits before sending it
  to the outer loop.
- [pass](https://www.passwordstore.org/) - For getting/storing secrets
- [gosmee](https://github.com/chmouel/gosmee) - For replaying webhooks

## Target architecture

- We target arm64 and amd64, the dogfooding is on arm64, so we need to make
sure that all jobs and docker images used in the .tekton PipelineRuns are built
for arm64.

# Links

- [Jira Backlog](https://issues.redhat.com/browse/SRVKP-2144?jql=component%20%3D%20%22Pipeline%20as%20Code%22%20%20AND%20status%20!%3D%20Done)
- [Bitbucket Server Rest API](https://docs.atlassian.com/bitbucket-server/rest/7.17.0/bitbucket-rest.html)
- [GitHub API](https://docs.github.com/en/rest/reference)
- [GitLab API](https://docs.gitlab.com/ee/api/api_resources.html)
