#!/usr/bin/env bash
# Generate a local CA + server cert for the Prism TLS front.
#
# The generated server URL is https://, so the mock must serve real TLS (Caddy
# terminates it with this cert). The integration tests trust the self-signed cert
# through the provider's own allowInsecure=true flag (A3) — not SSL_CERT_FILE, which
# Go's default transport ignores on macOS (it verifies against the keychain, which
# also rejects certs valid >398 days). So these certs just need to be a valid SAN'd
# cert for Caddy to serve; the CA-pinned secure path (allowInsecure=false) is a
# Tier-2 (test/e2e/) concern.
#
# Idempotent: regenerates every run. Output is git-ignored.
set -euo pipefail

cd "$(dirname "$0")"
mkdir -p certs
cd certs

# Certificate Authority.
openssl genrsa -out ca.key 2048 2>/dev/null
openssl req -x509 -new -nodes -key ca.key -sha256 -days 3650 \
  -subj "/CN=pulumi-unifi-mock-ca" -out ca.crt 2>/dev/null

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
echo "Wrote test/mock/certs/{ca.crt,server.crt,server.key}"
