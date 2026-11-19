#!/bin/sh
# Entrypoint of the extractor image: starts a fresh server in /work (a tmpfs, the image itself
# being read-only) with the jars of /input (the add-on, and the plugins it depends on), lets the
# exporter write /output/export.json, and stops. SKMC_EXPORT_TARGET names the add-on's plugin.
set -eu

if [ -z "${SKMC_EXPORT_TARGET:-}" ]; then
    echo "SKMC_EXPORT_TARGET is not set" >&2
    exit 2
fi
work="${SKMC_WORK_DIR:-/work}"
mkdir -p "$work/plugins"

# The large read-only parts (patched server, libraries) are linked, the rest copied.
for entry in /opt/server/* /opt/server/.[!.]*; do
    [ -e "$entry" ] || continue
    name="$(basename "$entry")"
    case "$name" in
        cache|libraries|versions|paper.jar) ln -sfn "$entry" "$work/$name" ;;
        plugins) cp -R "$entry/." "$work/plugins/" ;;
        *) cp -R "$entry" "$work/$name" ;;
    esac
done
found=0
for jar in /input/*.jar; do
    [ -e "$jar" ] || continue
    cp "$jar" "$work/plugins/"
    found=1
done
if [ "$found" -eq 0 ]; then
    echo "No jar in /input" >&2
    exit 2
fi

cd "$work"
status=0
java -Xms256M -Xmx"${SKMC_HEAP:-1G}" -XX:+UseSerialGC -Dcom.mojang.eula.agree=true \
    -Dpaper.disableChannelLimit=true -jar paper.jar --nogui </dev/null || status=$?

output="${SKMC_EXPORT_OUTPUT:-/output/export.json}"
if [ ! -s "$output" ]; then
    echo "The server stopped (exit code $status) without writing $output" >&2
    exit 3
fi
exit 0
