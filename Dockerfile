# Образ для GoReleaser (dockers_v2): бінарник уже зібраний з CGO_ENABLED=0
# у .goreleaser.yaml і лежить у контексті як $TARGETPLATFORM/keycrm-mcp.
# Локально: goreleaser release --snapshot --clean
#
# distroless/static:nonroot — без shell і пакетного менеджера, користувач 65532.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/keycrm-mcp /usr/local/bin/keycrm-mcp

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/keycrm-mcp"]
