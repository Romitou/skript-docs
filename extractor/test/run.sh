#!/usr/bin/env bash
# End-to-end check of an extractor image: runs it, sandboxed like the scanner does, on the test
# add-on (../test-addon) and checks what the exporter attributed to it.
#
#   extractor/test/run.sh [image]          (default skript-docs-extractor:26.3-skript2.17.1)
#
# Needs Docker, Maven (to build the test add-on) and Python 3. Used by .github/workflows/images.yml.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
IMAGE="${1:-skript-docs-extractor:26.3-skript2.17.1}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

(cd "$HERE/../test-addon" && mvn -q -B package)
mkdir -p "$WORK/input" "$WORK/output"
chmod 0755 "$WORK" "$WORK/input"
chmod 0777 "$WORK/output"
cp "$HERE/../test-addon/target/skmc-test-addon.jar" "$WORK/input/"

# Same sandbox as scanner/run.go (DockerArgs).
docker run --rm --network none --read-only \
  --tmpfs /work:rw,exec,size=1g,mode=1777 --tmpfs /tmp:rw,exec,size=256m,mode=1777 \
  --cap-drop ALL --security-opt no-new-privileges --pids-limit 512 --memory 1536m --memory-swap 1536m \
  --hostname skmc-extractor --add-host skmc-extractor:127.0.0.1 \
  -v "$WORK/input:/input:ro" -v "$WORK/output:/output" \
  -e SKMC_EXPORT_TARGET=SkmcTestAddon "$IMAGE" > "$WORK/server.log" 2>&1 || {
    tail -50 "$WORK/server.log"
    echo "The container failed" >&2
    exit 1
  }

python3 - "$WORK/output/export.json" <<'PY'
import json, sys

export = json.load(open(sys.argv[1]))
assert "error" not in export, export.get("error")
assert export["format"] == "skmc-syntax-export", export.get("format")
assert export["source"]["plugin"] == "SkmcTestAddon" and export["source"]["version"] == "1.2.3", export["source"]

def only(category):
    items = export[category]
    assert len(items) == 1, f"{category}: expected one element of the test add-on, got {[i['id'] for i in items]}"
    return items[0]

effect = only("effects")
assert effect["id"] == "EffTestShout" and effect["name"] == "Skmc Test Shout", effect
assert effect["patterns"] == ["skmc test shout %text%"] and effect["since"] == ["1.0"], effect
assert effect["examples"] == ['skmc test shout "hello"'] and effect["documented"], effect

expression = only("expressions")
assert expression["id"] == "ExprTestAnswer" and expression["name"] == "Test Answer", expression
assert not expression["documented"] and expression["returnType"] == "integer", expression

event = only("events")
assert event["name"] == "On Skmc test jump" and event["since"] == ["1.1"], event
assert event["cancellable"], event

kind = only("types")
assert kind["id"] == "skmctestthing" and kind["name"] == "Skmc Test Thing", kind

# Nothing of Skript's own: everything else is empty.
for category in ("conditions", "sections", "structures", "functions"):
    assert export[category] == [], f"{category}: {export[category]}"
stats = export["stats"]
# The event is on SimpleEvent with a Paper event: attributed without other proof, and counted.
assert stats["total"] == 4 and stats["undocumented"] == 1 and stats["ambiguous"] == 1, stats
print(f"OK: {stats['total']} elements of {export['source']['plugin']} on Minecraft {export['source']['minecraftVersion']}")
PY
