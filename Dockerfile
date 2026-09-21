# The official registry image (docs/distribution/docker-plan.md): assembly,
# not a build. The tacit binaries come prebuilt from the release workflow
# (dist/tacit-linux-<arch>, CGO + -tags onnx); this file fetches the pinned
# semantic-retrieval artifacts and lays everything onto distroless.
#
#   docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/opentacit/tacit .
#
# Base image choice: the binary is glibc-dynamic (CGO) and dlopens
# libonnxruntime.so, which needs glibc AND libstdc++ — that rules out
# scratch/static and Alpine/musl. distroless/cc is exactly glibc + libstdc++
# + CA certs: no shell, no package manager (~25MB). Run as nonroot.

# --- fetch: download the digest-pinned onnx runtime + model -----------------
# Runs the BUILD platform's binary so no emulation is needed; --platform
# selects which architecture's runtime library to fetch for the TARGET.
# Pinned by digest, not tag: a tag is a moving target, and an image that
# changes underneath a release is not reproducible. Update deliberately —
# `docker buildx imagetools inspect debian:12-slim` prints the current one.
FROM --platform=$BUILDPLATFORM debian:12-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171 AS fetch
ARG BUILDARCH
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY dist/tacit-linux-${BUILDARCH} /usr/local/bin/tacit
RUN /usr/local/bin/tacit onnx-fetch --dir /onnx --platform linux/${TARGETARCH} \
    && mkdir -p /empty

# --- final ------------------------------------------------------------------
# Likewise. This is the image the published registry actually runs as.
FROM gcr.io/distroless/cc-debian12:nonroot@sha256:9dac0a79194e45a7da0158a9c6da57b217585af0786db3845d1f0ec1a0dd182f
ARG TARGETARCH
COPY dist/tacit-linux-${TARGETARCH} /usr/local/bin/tacit
COPY --from=fetch /onnx/libonnxruntime.so /opt/tacit/libonnxruntime.so
# The runtime's own MIT licence and the notices for the thirty-odd components
# its binary statically links. Redistributing libonnxruntime.so without these
# is a licence violation, and this image redistributes it to everyone who pulls.
COPY --from=fetch /onnx/ONNXRUNTIME-LICENSE.txt /onnx/ONNXRUNTIME-ThirdPartyNotices.txt /opt/tacit/
COPY --from=fetch /onnx/model /opt/tacit/model
COPY docs /opt/tacit/docs
# Apache-2.0 §4(a) and §4(d): a redistribution carries the license and the
# NOTICE. The image redistributes the binary, the ONNX runtime and the model,
# so the attributions for those ride along too.
COPY LICENSE NOTICE THIRD-PARTY-NOTICES.md /opt/tacit/
# /data must pre-exist owned by nonroot (uid 65532): a named volume inherits
# the image path's ownership on first use, and distroless has no shell to
# chown later.
COPY --from=fetch --chown=65532:65532 /empty /data

# Everything mutable lives under the /data volume — including registry.env,
# because the first-run claim form and the Settings page WRITE configuration
# and those writes must survive a container replacement. The TACIT_ONNX_* and
# TACIT_EMBED_* values pin semantic retrieval to the baked artifacts (the onnx
# trap stays dead); note environment variables override registry.env, so
# changing these means changing the container's env, not the Settings page.
#
# Storage defaults to SQLite at /data/tacit.db (docs/design/sqlite-plan.md):
# a container is exactly the single-machine deployment that backend serves,
# and one database file beats a directory of JSONL for volume backups. A
# volume that predates this default is imported automatically on first boot
# (see sqliteAutoCutover; the old data dir stays in place as the archive).
# Override with container env: TACIT_DB_URL=postgres://... for shared
# deployments, TACIT_DB_URL="" to force the embedded file store.
ENV TACIT_CONTAINER=1 \
    TACIT_REGISTRY_ENV=/data/registry.env \
    TACIT_DATA=/data/data \
    TACIT_TECHNIQUES_DIR=/data/techniques \
    TACIT_DB_URL=sqlite:///data/tacit.db \
    TACIT_ONNX_LIB=/opt/tacit/libonnxruntime.so \
    TACIT_ONNX_MODEL_DIR=/opt/tacit/model \
    TACIT_EMBED_MODEL=onnx/all-MiniLM-L6-v2 \
    TACIT_EMBED_DIM=384

VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
    CMD ["/usr/local/bin/tacit", "doctor", "--probe"]
ENTRYPOINT ["/usr/local/bin/tacit", "serve", "--docs", "/opt/tacit/docs"]
