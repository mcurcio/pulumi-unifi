#!/usr/bin/env bash
# Generate a local CA + server cert for the e2e Caddy TLS front (mirrors
# test/mock/gen-certs.sh).
#
# Caddy terminates TLS on :443 (host :11443) with this cert; the live e2e tests
# trust it through the provider's allowInsecure=true flag (same as Tier-1) — not
# SSL_CERT_FILE, which Go's default transport ignores on macOS. So the cert just
# needs valid SANs (localhost / 127.0.0.1) for Caddy to serve. The CA-pinned
# secure path (allowInsecure=false) has its own no-Docker coverage.
#
# Idempotent: regenerates every run. Output (test/e2e/certs/) is git-ignored.
set -euo pipefail

cd "$(dirname "$0")"
mkdir -p certs
cd certs

# Certificate Authority.
openssl genrsa -out ca.key 2048 2>/dev/null
openssl req -x509 -new -nodes -key ca.key -sha256 -days 3650 \
  -subj "/CN=pulumi-unifi-e2e-ca" -out ca.crt 2>/dev/null

# Server key + CSR.
openssl genrsa -out server.key 2048 2>/dev/null
openssl req -new -key server.key -subj "/CN=localhost" -out server.csr 2>/dev/null

# Sign the server cert with SANs the provider connects by.
cat > san.ext <<'EOF'
subjectAltName = DNS:localhost, IP:127.0.0.1
extendedKeyUsage = serverAuth
EOF
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out server.crt -days 3650 -sha256 -extfile san.ext 2>/dev/null

rm -f server.csr san.ext ca.srl
echo "Wrote test/e2e/certs/{ca.crt,server.crt,server.key}"
