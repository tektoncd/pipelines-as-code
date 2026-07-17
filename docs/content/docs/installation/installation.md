---
title: Manual Installation
weight: 5
---

This page walks you through manually installing Pipelines-as-Code on Kubernetes or OpenShift clusters. Use this method when you need full control over the installation process or cannot use the OpenShift Pipelines Operator.

## Operator Install

Follow the [Operator Installation]({{< relref "openshift" >}}) guide to install Pipelines-as-Code on OpenShift.

## Manual Install

### Prerequisite

Before installing Pipelines-as-Code, verify that
[tektoncd/pipeline](https://github.com/tektoncd/pipeline) is installed. You can
install the latest released version with the following command:

```shell
  kubectl apply --filename https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
```

{{< callout type="info" >}}
If you are not installing the most recent version, ensure that you have Tekton Pipeline installed and running at a version higher than v0.44.0.
{{< /callout >}}

To install the stable release of Pipelines-as-Code manually, apply the
appropriate manifest with kubectl:

```shell
# OpenShift
kubectl patch tektonconfig config --type="merge" -p '{"spec": {"platforms": {"openshift":{"pipelinesAsCode": {"enable": false}}}}}'
kubectl apply -f https://raw.githubusercontent.com/tektoncd/pipelines-as-code/stable/release.yaml

# Kubernetes
kubectl apply -f https://raw.githubusercontent.com/tektoncd/pipelines-as-code/stable/release.k8s.yaml
```

To install the current development version, apply the nightly manifest:

```shell
# OpenShift
kubectl apply -f https://raw.githubusercontent.com/tektoncd/pipelines-as-code/nightly/release.yaml

# Kubernetes
kubectl apply -f https://raw.githubusercontent.com/tektoncd/pipelines-as-code/nightly/release.k8s.yaml
```

Applying the manifest creates the `pipelines-as-code` admin namespace, the required roles, and all other resources.

The `pipelines-as-code` namespace is where the Pipelines-as-Code infrastructure
runs. Only cluster administrators should have access to this namespace.

### OpenShift

On OpenShift, Pipelines-as-Code automatically creates the Route URL for the controller when
you apply the `release.yaml`. You need to reference this URL when configuring
your Git provider.

Run this command to retrieve the route URL for your cluster:

```shell
echo https://$(oc get route -n pipelines-as-code pipelines-as-code-controller -o jsonpath='{.spec.host}')
```

### Kubernetes

Kubernetes installation requires additional ingress setup. See the [Kubernetes installation guide]({{< relref "kubernetes" >}}) for details.

## RBAC

RBAC (Role-Based Access Control) governs which users can perform actions in a Kubernetes cluster. Non-`system:admin` users must be explicitly granted permission to create Repository
CRs in their namespace.

To grant this permission, create a `RoleBinding` on the namespace that references the
`openshift-pipeline-as-code-clusterrole`.

For example, to allow the user `user` to create Repository CRs in the
namespace `user-ci`, run the following with the OpenShift `oc` CLI:

```shell
oc adm policy add-role-to-user openshift-pipeline-as-code-clusterrole user -n user-ci
```

Alternatively, apply this YAML with kubectl:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: openshift-pipeline-as-code-clusterrole
  namespace: user-ci
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: openshift-pipeline-as-code-clusterrole
subjects:
- apiGroup: rbac.authorization.k8s.io
  kind: User
  name: user
```

## CLI

Pipelines-as-Code provides a CLI designed to work as a `tkn pac` plug-in. To
install the plug-in, follow the instructions in the [CLI]({{< relref "/docs/cli" >}})
documentation.

## Controller TLS Setup

The Pipelines-as-Code controller supports both `HTTP` and `HTTPS`. In most setups, you terminate TLS at the Ingress or Route level. However, if you need end-to-end encryption, you can configure TLS directly on the controller so that it serves HTTPS traffic itself.

To enable TLS on the controller, first create a Kubernetes Secret containing your TLS certificate and private key:

```shell
  kubectl create secret generic -n pipelines-as-code pipelines-as-code-tls-secret \
    --from-file=cert=/path/to/crt/file \
    --from-file=key=/path/to/key/file
```

After creating the secret, restart the `pipelines-as-code-controller` pod in the `pipelines-as-code` namespace. The controller detects the TLS secret on startup and begins serving HTTPS traffic.

{{< callout type="info" >}}

- The secret must be named `pipelines-as-code-tls-secret`, or you must update the secret name in the controller deployment.
- If you use different keys in your secret other than `cert` and `key`, update the controller deployment environment variables and reapply these changes on upgrade (for example through [kustomize](https://kustomize.io/) or other methods).
{{< /callout >}}

Use the following command to update the environment variables on the controller:

```shell
  kubectl set env deployment pipelines-as-code-controller -n pipelines-as-code TLS_KEY=<key> TLS_CERT=<cert>
```

## Proxy Service for the Controller

Pipelines-as-Code requires an externally accessible URL to receive events from
Git providers. If you are developing locally (such as on kind or Minikube) or
cannot set up an Ingress on your cluster, you can use a proxy service to
forward webhook events to the `pipelines-as-code-controller` service.

This approach is useful for testing and development but is not recommended for
production, because gosmee and the platform running
<https://hook.pipelinesascode.com>
provide no support or security guarantees.

### Proxying with hook.pipelinesascode.com

To set up webhook forwarding for a minikube or kind cluster, use [hook.pipelinesascode.com](https://hook.pipelinesascode.com/):

- Generate your own URL by going to [hook.pipelinesascode.com/new](https://hook.pipelinesascode.com/new).
- Copy the `Webhook Proxy URL`.
- Replace the placeholder in the container args of the `deployment.yaml` below with your generated URL.

For example: `'<replace Webhook Proxy URL>'` becomes `'https://hook.pipelinesascode.com/oLHu7IjUV4wGm2tJ'`

```yaml
kind: Deployment
apiVersion: apps/v1
metadata:
  name: gosmee-client
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gosmee-client
  template:
    metadata:
      creationTimestamp: null
      labels:
        app: gosmee-client
    spec:
      containers:
        - name: gosmee-client
          image: 'ghcr.io/chmouel/gosmee:main'
          args:
            - --output
            - json
            - --log-level
            - debug
            - --target-connection-timeout
            - "5"
            - --target-retries
            - "5"
            - '<replace Webhook Proxy URL>'
            - $(SVC)
          env:
            - name: SVC
              value: >-
                http://pipelines-as-code-controller.pipelines-as-code.svc.cluster.local:8080
      restartPolicy: Always
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 25%
      maxSurge: 25%
  revisionHistoryLimit: 10
  progressDeadlineSeconds: 600
```

- Apply the deployment:

```yaml
kubectl create -f deployment.yaml -n pipelines-as-code
```

- Use the `Webhook Proxy URL` when configuring your Git provider (GitHub, GitLab, or Bitbucket).

Provide the `Webhook Proxy URL` everywhere the `pipelines-as-code-controller` service URL is normally required.
