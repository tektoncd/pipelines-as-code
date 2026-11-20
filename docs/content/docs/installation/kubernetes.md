---
title: Kubernetes
weight: 3
---

This page walks you through installing and configuring Pipelines-as-Code on a Kubernetes cluster, including minikube and kind setups. Follow these steps if you are running standard Kubernetes rather than OpenShift.

## Prerequisites

Before you begin, ensure that:

- Your cluster runs Kubernetes version 1.28 or higher.
- You have installed Tekton Pipelines by applying the
[pipeline](https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml)
`release.yaml` file on your cluster.

## Install

To install the stable release of Pipelines-as-Code, apply the release manifest:

```shell
kubectl apply -f https://raw.githubusercontent.com/tektoncd/pipelines-as-code/stable/release.k8s.yaml
```

To install the nightly (development) build instead:

```shell
kubectl apply -f https://raw.githubusercontent.com/tektoncd/pipelines-as-code/nightly/release.k8s.yaml
```

## Verify

After applying the manifest, confirm that all three Pipelines-as-Code deployments (controller, webhook, and watcher) are running:

```shell
$ kubectl get deployment -n pipelines-as-code
NAME                           READY   UP-TO-DATE   AVAILABLE   AGE
pipelines-as-code-controller   1/1     1            1           43h
pipelines-as-code-watcher      1/1     1            1           43h
pipelines-as-code-webhook      1/1     1            1           43h
```

All three deployments should show all pods as ready before you proceed to Ingress setup.

## Ingress

An [Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/) is a Kubernetes resource that exposes HTTP/HTTPS routes from outside the cluster to services within it. You need an Ingress to make the Pipelines-as-Code controller reachable by your Git provider so it can deliver webhook events.

The Ingress configuration varies depending on your Kubernetes distribution. See the examples below for common setups.

You can use either the Ingress hostname or its IP address as the webhook URL when connecting Pipelines-as-Code to your Git provider. Retrieve the address with:
`kubectl get ingress pipelines-as-code -n pipelines-as-code`.

If you want to try Pipelines-as-Code without setting up an Ingress, the `tkn pac bootstrap` [CLI]({{< relref "/docs/cli" >}}) command sets up
a [gosmee](https://github.com/chmouel/gosmee) deployment using the
webhook URL remote forwarder `https://hook.pipelinesascode.com`.

### [GKE](https://cloud.google.com/kubernetes-engine)

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  labels:
    pipelines-as-code/route: controller
  name: pipelines-as-code
  namespace: pipelines-as-code
  annotations:
    kubernetes.io/ingress.class: gce
spec:
  defaultBackend:
    service:
      name: pipelines-as-code-controller
      port:
        number: 8080
```

### [Nginx Ingress Controller](https://kubernetes.github.io/ingress-nginx/)

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  labels:
    pipelines-as-code/route: controller
  name: pipelines-as-code
  namespace: pipelines-as-code
spec:
  ingressClassName: nginx
  rules:
  - host: webhook.host.tld
    http:
      paths:
      - backend:
          service:
            name: pipelines-as-code-controller
            port:
              number: 8080
        path: /
        pathType: Prefix
```

In this example, replace `webhook.host.tld` with the actual hostname for your Pipelines-as-Code
controller. You provide this hostname as the webhook URL in your Git provider configuration.

## Tekton Dashboard Integration

If you have [Tekton Dashboard](https://github.com/tektoncd/dashboard) installed,
you can link PipelineRun logs directly from your Git provider. Add the key `tekton-dashboard-url` in the `pipelines-as-code` ConfigMap,
set to the full URL of the Ingress host, to enable Tekton Dashboard log URLs.
