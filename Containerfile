FROM registry.access.redhat.com/ubi9/go-toolset:9.7-1769430014@sha256:359dd4c6c4255b3f7bce4dc15ffa5a9aa65a401f819048466fa91baa8244a793 as builder

ARG BUILD_TARGET=pipelines-as-code-controller

USER 1001
WORKDIR /opt/app-root/src

COPY --chown=1001:0 . .

RUN CGO_ENABLED=0 go build -o /opt/app-root/src/binary ./cmd/${BUILD_TARGET}

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.7-1764578379
COPY --from=builder /opt/app-root/src/binary /usr/local/bin/pipelines-as-code

LABEL name="pipelines-as-code"
LABEL com.redhat.component="pipelines-as-code"
LABEL description="Pipelines as Code"
LABEL io.k8s.description="Pipelines as Code"
LABEL io.openshift.tags="konflux"

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/pipelines-as-code"]
