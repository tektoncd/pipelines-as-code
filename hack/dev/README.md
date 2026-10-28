# How to debug

## Preamble

* Install Pipelines as Code on Kind or on Openshift (both are supported)
* Create your own Github APP point webhook to the eventlistenner route/ingress.
* Create your own test repository
* plug test repository into your Github APP
* Configure your app on your test repository.
* Do a pull request with a .tekton and a pipelinerun
* Make sure everything works and PAC is running pull request.

### VSCode

* Add this task on vscode :

```json
    {
      "name": "PAC Controller",
      "type": "go",
      "request": "launch",
      "mode": "auto",
      "program": "${workspaceFolder}/cmd/pipelines-as-code-controller",
      "env": {
        "KUBECONFIG": "${env:HOME}/.kube/config",
        "SYSTEM_NAMESPACE": "pipelines-as-code",
      },
    },
```

### Goland

* Add a new "Go build" configuration.
* Package Path to `github.com/tektoncd/pipelines-as-code/cmd/pipelines-as-code-controller`
* Environment variables to: `KUBECONFIG=$HOME/.kube/config;SYSTEM_NAMESPACE=pipelines-as-code`

## Run

* adjust the KUBECONFIG if needed.
* Run the debug configuration, it should wait and listen.

## Scripts

### GitHub

You can use the script [gh-apps-events.py](./gh-apps-events.py) to replay a
event from your github app.

Make sure it points to the localhost:8080 with the option `"-e`", this is where
the controller running in debug mode on your IDE i.e:

`./hack/dev/replay-gh-events.py -e http://localhost:8080`

It will auto detect the secrets and application_id/webhook secret from the
running secret in the `pipelines-as-code`namespace.

It will show you the last 10 events and which one to choose and replay it or
choose `"-l`" to play directly the last one. It will replay in your controller
that is running in debugging mode in your IDE which should catch the
breakpoints etc...

You can as well with that script save the replay in a python script, just add
the option "--save PATH" and after launching and choosing an event there would be a
file generated to PATH that would replay it on the controller.
