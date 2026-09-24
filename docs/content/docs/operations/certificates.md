---
title: Custom certificates
weight: 5
---

This page explains how to configure Pipelines-as-Code to use custom or privately signed TLS certificates. You need this when your Git repository sits behind a certificate authority that the default system trust store does not include.

## OpenShift

If you installed Pipelines-as-Code through the OpenShift Pipelines operator, [add your custom certificate to the cluster via the Proxy object](https://docs.openshift.com/container-platform/4.11/networking/configuring-a-custom-pki.html#nw-proxy-configure-object_configuring-a-custom-pki). The operator automatically makes the certificate available to all OpenShift Pipelines components and workloads, including Pipelines-as-Code.

## Kubernetes

On Kubernetes, you must manually create a ConfigMap with your certificate, mount it into the controller and watcher pods, and add the mount path to the trusted certificate directories.

### Create a ConfigMap containing the certificate

```shell
kubectl -n pipelines-as-code create configmap git-repo-cert --from-file=git.crt=<path to ca.crt>
```

### Mount the ConfigMap in the pods

Follow [this guide](https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/#add-configmap-data-to-a-volume) to mount the ConfigMap as a volume in both the `pipelines-as-code-controller` and `pipelines-as-code-watcher` Deployments in the `pipelines-as-code` namespace.

### Add the mount path to `SSL_CERT_DIR`

After mounting the ConfigMap, you need to tell the processes where to find your certificate. For example, if you chose `/pac-custom-certs` as the `mountPath`, set the `SSL_CERT_DIR` environment variable on the relevant Deployments so that it includes your custom path alongside the default system paths:

```shell
kubectl set env deployment pipelines-as-code-controller pipelines-as-code-watcher -n pipelines-as-code SSL_CERT_DIR=/pac-custom-certs:/etc/ssl/certs:/etc/pki/tls/certs:/system/etc/security/cacerts
```

Once you apply this change, Pipelines-as-Code can access your Git repository using the custom certificate.

Heres a YAML snippet showing  where to place the env and volumeMounts for the pipelines-as-code-controller  kubernetes section within the TektonConfig custom resource:

```yaml
...
platforms:
    kubernetes:   
      pipelinesAsCode:
        options:
            deployments:
                pipelines-as-code-controller:
                    spec:
                        template:
                            spec:
                                containers:
                                    name: pac-controller
                                    - env:
                                        - name: SSL_CERT_DIR
                                          value: /etc/ssl/certs/custom
                                        volumeMounts:
                                        - name: custom-cert
                                          mountPath: /etc/ssl/certs/custom
                                volumes:
                                    name: custom-certs
                                    - config-map: 
                                        name: ca-certs

```

