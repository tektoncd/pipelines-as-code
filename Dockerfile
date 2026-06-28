FROM golang:1.24 as builder
ARG BINARY_NAME=pipelines-as-code-controller
COPY . /src
WORKDIR /src
RUN \
  git config --global --add safe.directory /src && \
  make /tmp/${BINARY_NAME} LDFLAGS="-s -w" OUTPUT_DIR=/tmp

FROM registry.access.redhat.com/ubi9/ubi-minimal

ARG BINARY_NAME=pipelines-as-code-controller
LABEL com.redhat.component=${BINARY_NAME} \
  name=openshift-pipelines/${BINARY_NAME} \
  maintainer=pipelines@redhat.com \
  summary="This image is to run Pipelines as Code ${BINARY_NAME} component"

COPY --from=builder /tmp/${BINARY_NAME} /usr/bin/${BINARY_NAME}

USER 1001
ENV RUN_BINARY_NAME=$BINARY_NAME
CMD /usr/bin/${RUN_BINARY_NAME}
