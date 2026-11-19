# syntax=docker/dockerfile:1.7
#
# Syntax extractor: Paper + Skript + the exporter plugin (../exporter), ready to start without
# network access. The scanner (../../scanner) runs one throwaway container of this image per add-on
# version, with the add-on's jar mounted in /input, and reads /output/export.json.
# See ../../README.md. Built and published by .github/workflows/images.yml, from images.json.
#
# One image per Minecraft/Skript pair (extractor/images.json), picked by the scanner from the
# add-on's api-version:
#   docker build -t skript-docs-extractor:26.3-skript2.17.1 -f extractor/image/Dockerfile extractor
#   docker build -t skript-docs-extractor:1.21.11-skript2.17.1 --build-arg JAVA_VERSION=21 --build-arg PAPER_VERSION=1.21.11 \
#       -f extractor/image/Dockerfile extractor
# (the build context is extractor/, for the exporter's sources).
#
# Every download is checked against the checksum its source publishes. Behind a TLS-intercepting
# proxy, pass its CA as a build secret (--secret id=extra_ca,src=ca.crt): build-env.sh trusts it,
# and passes HTTPS_PROXY on to the JVMs.

ARG JAVA_VERSION=25
ARG PAPER_VERSION=26.3
ARG SKRIPT_VERSION=2.17.1

# --- The exporter plugin -------------------------------------------------------------------------
FROM maven:3.9-eclipse-temurin-21 AS exporter
ARG SKRIPT_VERSION
ARG HTTPS_PROXY
COPY image/build-env.sh /usr/local/bin/build-env.sh
WORKDIR /build
COPY exporter/pom.xml .
COPY exporter/src src
RUN --mount=type=secret,id=extra_ca --mount=type=cache,target=/root/.m2 \
    . build-env.sh && mvn -q -B package -Dskript.version="${SKRIPT_VERSION}"

# --- Paper and Skript downloads ------------------------------------------------------------------
FROM alpine:3.23 AS downloads
ARG PAPER_VERSION
ARG SKRIPT_VERSION
ARG HTTPS_PROXY
RUN apk add --no-cache curl jq
COPY image/build-env.sh /usr/local/bin/build-env.sh
WORKDIR /dl
# Latest build of PAPER_VERSION from Paper's download API (Fill v3), with the sha256 it publishes.
RUN --mount=type=secret,id=extra_ca . build-env.sh \
    && curl -fsSL "https://fill.papermc.io/v3/projects/paper/versions/${PAPER_VERSION}/builds/latest" -o build.json \
    && jq -r '.downloads["server:default"].url' build.json > paper.url \
    && jq -r '.downloads["server:default"].checksums.sha256' build.json > paper.sha256 \
    && curl -fsSL "$(cat paper.url)" -o paper.jar \
    && echo "$(cat paper.sha256)  paper.jar" | sha256sum -c - \
    && jq -r '"Paper \(.id)"' build.json > paper.build
RUN --mount=type=secret,id=extra_ca . build-env.sh \
    && base="https://repo.skriptlang.org/releases/com/github/SkriptLang/Skript/${SKRIPT_VERSION}/Skript-${SKRIPT_VERSION}.jar" \
    && curl -fsSL "$base" -o Skript.jar \
    && echo "$(curl -fsSL "$base.sha256")  Skript.jar" | sha256sum -c -

# --- The image ------------------------------------------------------------------------------------
FROM eclipse-temurin:${JAVA_VERSION}-jre
ARG PAPER_VERSION
ARG SKRIPT_VERSION
ARG HTTPS_PROXY
LABEL org.opencontainers.image.title="skmc-extractor" \
      org.opencontainers.image.description="Exports the syntaxes of a Skript add-on (skript-docs)" \
      fr.skript-mc.extractor.minecraft="${PAPER_VERSION}" \
      fr.skript-mc.extractor.skript="${SKRIPT_VERSION}"

COPY image/build-env.sh /usr/local/bin/build-env.sh
COPY image/run.sh /usr/local/bin/skmc-extract
COPY image/server/ /opt/server/
COPY --from=downloads /dl/paper.jar /opt/server/paper.jar
COPY --from=downloads /dl/Skript.jar /opt/server/plugins/Skript.jar
COPY --from=exporter /build/target/skmc-syntax-exporter.jar /opt/server/plugins/skmc-syntax-exporter.jar

# First start, at build time: Paper downloads and patches the Minecraft server, Skript writes its
# configuration and aliases, and the exporter records what Skript registers on its own
# (/opt/skmc/baseline.json). The containers then start offline. The world is thrown away.
RUN --mount=type=secret,id=extra_ca . build-env.sh \
    && chmod 0755 /usr/local/bin/skmc-extract \
    && mkdir -p /opt/skmc \
    && cd /opt/server \
    && SKMC_EXPORT_MODE=baseline SKMC_EXPORT_BASELINE=/opt/skmc/baseline.json \
       java -Xmx1G -Dcom.mojang.eula.agree=true -jar paper.jar --nogui \
    && test -s /opt/skmc/baseline.json \
    && rm -rf world world_nether world_the_end logs crash-reports usercache.json \
    && printf 'minecraft=%s\nskript=%s\n' "${PAPER_VERSION}" "${SKRIPT_VERSION}" > /opt/skmc/versions \
    && chmod -R a+rX /opt/server /opt/skmc

ENV SKMC_EXPORT_BASELINE=/opt/skmc/baseline.json \
    SKMC_EXPORT_OUTPUT=/output/export.json \
    JAVA_TOOL_OPTIONS=""
USER 1000:1000
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/skmc-extract"]
