#!/usr/bin/env -S uv --quiet run --script
# /// script
# requires-python = ">=3.12"
# dependencies = [
#     "requests",
#     "PyYAML",
# ]
# ///
# Author: Chmouel Boudjnah <chmouel@redhat.com>
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may
# not use this file except in compliance with the License. You may obtain
# a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
# WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
# License for the specific language governing permissions and limitations
# under the License.

import argparse
import os
import sys

import yaml

HELP_DESC = """This script will add a second controller to pipelines-as-code

It's designed to be used like this:

kubectl apply -f <(python3 yamlconvert.py LABEL)

If you want to build the controller with ko you can use the
--controller-image=ko, this will (assuming you have a KO_DOCKER_REPO env
variable set properly) use ko to deploy the second controller image in the
target namespace (default: pipelines-as-code):

ko apply -f <(python3 yamlconvert.py --controller-image=ko LABEL)

You can define a few env variables to change the behavior:

• PAC_CONTROLLER_LABEL: the label of the controller
• PAC_CONTROLLER_TARGET_NS: the namespace to deploy the controller to (default:
                            pipelines-as-code)
• PAC_CONTROLLER_SECRET: the name of the secret to use (default: LABEL-secret)
• PAC_CONTROLLER_CONFIGMAP: the name of the configmap to use (default: LABEL-configmap)
• PAC_CONTROLLER_SMEE_URL: the url to use if you want to deploy a gosmee
                           container to this controller. If not set, it won't deploy it.
• PAC_CONTROLLER_IMAGE: the image name of the controller. use the word "ko" to use ko to build the image
                       (default: ghcr.io/tektoncd/pipelines-as-code/pipelines-as-code-controller:stable)
"""


def parse_arguments():
    parser = argparse.ArgumentParser(
        description="Generate yaml to add second paac controller",
        epilog=HELP_DESC,
        formatter_class=argparse.RawTextHelpFormatter,
    )
    parser.add_argument(
        "label",
        metavar="LABEL",
        help="Label to use for the controller",
        default=os.environ.get("PAC_CONTROLLER_LABEL"),
    )
    parser.add_argument(
        "--configmap",
        help="name of the configmap to use for the controller, default label-configmap",
        default=os.environ.get("PAC_CONTROLLER_CONFIGMAP"),
    )

    parser.add_argument(
        "--ingress-domain",
        help="if set this will add an ingress to the generated controller",
        default=os.environ.get("PAC_CONTROLLER_INGRESS_DOMAIN"),
    )

    parser.add_argument(
        "--secret",
        help="name of the secret to use for the controller, default label-secret",
        default=os.environ.get("PAC_CONTROLLER_SECRET"),
    )

    parser.add_argument(
        "--controller-image",
        help="use this image for the controller, instead of the default ones, (use the keyword ko for ko)",
        default=os.environ.get(
            "PAC_CONTROLLER_IMAGE",
            "ghcr.io/tektoncd/pipelines-as-code/pipelines-as-code-controller:stable",
        ),
    )

    parser.add_argument(
        "--gosmee-image",
        help="use this image instead for gosmee",
        default="ghcr.io/chmouel/gosmee:main",
    )

    parser.add_argument(
        "--smee-url",
        help="if set this will deploy a gosmee container to that smee_url and redirect query to the new controller to it",
        default=os.environ.get("PAC_CONTROLLER_SMEE_URL"),
    )

    parser.add_argument(
        "--namespace",
        help="namespace where pac is installed",
        default=os.environ.get("PAC_CONTROLLER_TARGET_NS", "pipelines-as-code"),
    )

    parser.add_argument(
        "--openshift-route",
        help="add an openshift route to the controller",
        action="store_true",
    )
    return parser.parse_args()


args = parse_arguments()
if not args.configmap:
    args.configmap = f"{args.label}-configmap"

if not args.secret:
    args.secret = f"{args.label}-secret"

controller = {}
with open("config/400-controller.yaml", "r", encoding="utf-8") as f:
    controller = yaml.load(f, Loader=yaml.FullLoader)
    controller["spec"]["selector"]["matchLabels"]["app.kubernetes.io/name"] = (
        args.label + "-controller"
    )
    controller["metadata"]["name"] = args.label + "-controller"
    controller["metadata"]["namespace"] = args.namespace
    controller["spec"]["template"]["metadata"]["labels"]["app"] = (
        args.label + "-controller"
    )
    controller["spec"]["template"]["metadata"]["labels"]["app.kubernetes.io/name"] = (
        args.label + "-controller"
    )
    for container in controller["spec"]["template"]["spec"]["containers"]:
        if container["name"] == "pac-controller":
            container["name"] = args.label + "-controller"
            if args.controller_image and args.controller_image != "ko":
                container["image"] = args.controller_image
            for env in container["env"]:
                if env["name"] == "PAC_CONTROLLER_LABEL":
                    env["value"] = args.label + "-controller"
                if env["name"] == "PAC_CONTROLLER_SECRET":
                    env["value"] = args.secret
                if env["name"] == "PAC_CONTROLLER_CONFIGMAP":
                    env["value"] = args.configmap

service = {}
with open("config/401-controller-service.yaml", "r", encoding="utf-8") as f:
    service = yaml.load(f, Loader=yaml.FullLoader)
    service["spec"]["selector"]["app.kubernetes.io/name"] = args.label + "-controller"
    service["metadata"]["name"] = args.label + "-controller"
    service["metadata"]["namespace"] = args.namespace
    service["metadata"]["labels"]["app"] = args.label
    service["metadata"]["labels"]["app.kubernetes.io/name"] = args.label + "-controller"

configmap = {}
with open("config/302-pac-configmap.yaml", "r", encoding="utf-8") as f:
    configmap = yaml.load(f, Loader=yaml.FullLoader)
    configmap["metadata"]["name"] = args.configmap
    configmap["metadata"]["namespace"] = args.namespace

# re-encode as YAML to stdout
for obj in [controller, service, configmap]:
    print("---")
    yaml.dump(obj, sys.stdout, default_flow_style=False)

if args.smee_url:
    sys.stdout.write(
        f"""---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gosmee-{args.label}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gosmee-{args.label}
  template:
    metadata:
      labels:
        app: gosmee-{args.label}
    spec:
      containers:
        - image: {args.gosmee_image}
          imagePullPolicy: Always
          name: gosmee
          args:
            [
              "client",
              "-o",
              "json",
              "--log-level",
              "debug",
              "--target-connection-timeout",
              "5",
              "--target-retries",
              "5",
              "--saveDir",
              "/tmp/gosmee-replay-{args.label}",
              "{args.smee_url}",
              "http://{args.label}-controller.{args.namespace}:8080",
            ]
    """
    )

if args.ingress_domain:
    sys.stdout.write(
        f"""
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {args.label}-controller
  namespace: {args.namespace}
  labels:
    pipelines-as-code/route: {args.label}
spec:
  ingressClassName: nginx
  rules:
  - host: "{args.label}.{args.ingress_domain}"
    http:
      paths:
      - backend:
          service:
            name: "{args.label}-controller"
            port:
              number: 8080
        path: /
        pathType: Prefix
"""
    )


if args.openshift_route:
    with open("config/openshift/10-routes.yaml", "r", encoding="utf-8") as f:
        fname = args.label + "-controller"
        route = yaml.load(f, Loader=yaml.FullLoader)
        route["metadata"]["name"] = fname
        route["metadata"]["labels"]["app"] = fname
        route["metadata"]["labels"]["pipelines-as-code/route"] = (
            args.label + "-controller"
        )
        route["spec"]["to"]["name"] = fname
        print("---")
        yaml.dump(route, sys.stdout, default_flow_style=False)
