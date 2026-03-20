FROM registry.access.redhat.com/ubi9/go-toolset:9.7-1773851748@sha256:8b211cc8793d8013140844e8070aa584a8eb3e4e6e842ea4b03dd9914b1a5dc6 AS builder

ARG BUILD_TARGET=pipelines-as-code-controller

USER 1001
WORKDIR /opt/app-root/src

COPY --chown=1001:0 . .

RUN CGO_ENABLED=0 go build -o /opt/app-root/src/binary ./cmd/${BUILD_TARGET}

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.7-1764578379
ARG BUILD_TARGET=pipelines-as-code-controller
COPY --from=builder /opt/app-root/src/binary /ko-app/${BUILD_TARGET}

LABEL name="pipelines-as-code"
LABEL com.redhat.component="pipelines-as-code"
LABEL description="Pipelines as Code"
LABEL io.k8s.description="Pipelines as Code"
LABEL io.openshift.tags="konflux"

USER 65532:65532

ENTRYPOINT ["/ko-app/${BUILD_TARGET}"]
