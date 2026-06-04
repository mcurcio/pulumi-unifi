# Thin, version-LOCKED UniFi Network Application controller for the Tier-2 e2e
# harness. Same idea as linuxserver/unifi-network-application, but pinned to the
# EXACT spec version (10.4.57) the provider is generated against, so the live
# round-trip runs on the same controller the schema describes — no version gap.
#
# Pin-and-verify ethos (cf. openapi/fetch.sh / openapi/pin.env): the .deb is
# downloaded from Ubiquiti's CDN and checksum-verified at build time against a
# trust-on-first-use sha256 baked below. If the upstream artifact ever changes
# under the same version path, `sha256sum -c` fails the build — the pin is the
# source of truth, not "whatever the URL serves today".
#
#   Pinned artifact : https://dl.ui.com/unifi/10.4.57/unifi_sysvinit_all.deb
#   Pinned sha256   : fc378cf8cd2bec3d334bf7b72eabfcd1861e5fae67b9c16735471132105b2072
#
# To re-pin on a version bump: change UNIFI_VERSION, download the new .deb,
# `sha256sum` it, set UNIFI_SHA256 to that value, and update test/e2e/README.md.
#
# Java: 10.4.57's launcher (com.ubnt.ace.Launcher) is class file version 69.0 =
# Java 25 (UnsupportedClassVersionError under Java 17/21). The .deb's own
# `Depends:` confirms it (temurin-25-jre | openjdk-25-jre-headless | ...). So the
# base is the Temurin 25 JRE, NOT 17.
FROM eclipse-temurin:25-jre-jammy

# --- pin ---------------------------------------------------------------------
ARG UNIFI_VERSION=10.4.57
ARG UNIFI_SHA256=fc378cf8cd2bec3d334bf7b72eabfcd1861e5fae67b9c16735471132105b2072

# --- runtime deps ------------------------------------------------------------
# We install the controller via `dpkg-deb -x` (a plain extract — no maintainer
# scripts, no systemd, no auto-pulled apt Depends), so we add by hand only what
# the running Java process actually needs:
#   ca-certificates  - TLS trust for outbound + the controller's own keystore.
#   fontconfig + libfreetype6 - Java AWT/2D pulls these even headless; their
#                       absence makes parts of the app's startup throw.
#   tzdata           - timezone DB (TZ env honoured).
#   binutils         - provides objcopy/ld, used by bundled native helpers.
#   curl             - in-container readiness probing (compose healthcheck).
# linuxserver's image installs the same general set; this is the minimal subset
# that makes the bare extract boot.
RUN apt-get update \
 && apt-get install --no-install-recommends -y \
      ca-certificates \
      fontconfig \
      libfreetype6 \
      tzdata \
      binutils \
      curl \
 && rm -rf /var/lib/apt/lists/*

# --- fetch + verify + extract the pinned controller --------------------------
# Download once, verify the trust-on-first-use sha256 (build FAILS on mismatch),
# then `dpkg-deb -x` extract to a STAGING dir and move only /usr/lib/unifi into
# place. (Extracting straight onto / mid-RUN can shadow the base image's own
# paths; staging keeps the layer clean.) The app lands in /usr/lib/unifi with
# its data dir at /usr/lib/unifi/data. dpkg-deb ships in the base (debian/ubuntu).
RUN set -eux; \
    url="https://dl.ui.com/unifi/${UNIFI_VERSION}/unifi_sysvinit_all.deb"; \
    curl -fSL "$url" -o /tmp/unifi.deb; \
    echo "${UNIFI_SHA256}  /tmp/unifi.deb" | sha256sum -c -; \
    mkdir -p /tmp/unifi-stage; \
    dpkg-deb -x /tmp/unifi.deb /tmp/unifi-stage; \
    mkdir -p /usr/lib; \
    mv /tmp/unifi-stage/usr/lib/unifi /usr/lib/unifi; \
    rm -rf /tmp/unifi.deb /tmp/unifi-stage; \
    test -f /usr/lib/unifi/lib/ace.jar; \
    mkdir -p /usr/lib/unifi/data /usr/lib/unifi/logs /usr/lib/unifi/run

# --- entrypoint --------------------------------------------------------------
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# The Network app serves its GUI + the /integration/v1 API over HTTPS on 8443.
EXPOSE 8443
WORKDIR /usr/lib/unifi
ENTRYPOINT ["/entrypoint.sh"]
