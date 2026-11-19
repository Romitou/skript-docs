# Sourced by the Dockerfile's RUN steps that reach the network. Only useful behind a
# TLS-intercepting proxy: trusts its CA (build secret "extra_ca") for curl and the JVMs, and gives
# HTTPS_PROXY to the JVMs, which ignore the variable. Does nothing otherwise.

if [ -s /run/secrets/extra_ca ]; then
    export CURL_CA_BUNDLE=/tmp/build-ca.pem
    cat /etc/ssl/certs/ca-certificates.crt /run/secrets/extra_ca > "$CURL_CA_BUNDLE" 2>/dev/null \
        || cp /run/secrets/extra_ca "$CURL_CA_BUNDLE"
    if command -v keytool >/dev/null 2>&1; then
        cp "${JAVA_HOME}/lib/security/cacerts" /tmp/build-truststore
        # keytool imports one certificate per file: split the bundle.
        rm -rf /tmp/build-ca && mkdir -p /tmp/build-ca
        awk '/BEGIN CERTIFICATE/{n++} n{print > ("/tmp/build-ca/" n ".pem")}' /run/secrets/extra_ca
        for cert in /tmp/build-ca/*.pem; do
            keytool -importcert -noprompt -alias "build-extra-ca-$(basename "$cert" .pem)" -file "$cert" \
                -keystore /tmp/build-truststore -storepass changeit >/dev/null 2>&1 || true
        done
        export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Djavax.net.ssl.trustStore=/tmp/build-truststore -Djavax.net.ssl.trustStorePassword=changeit"
    fi
fi

if [ -n "${HTTPS_PROXY:-}" ]; then
    proxy="${HTTPS_PROXY#*://}"
    proxy="${proxy%%/*}"
    export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Dhttps.proxyHost=${proxy%:*} -Dhttps.proxyPort=${proxy##*:}"
fi
