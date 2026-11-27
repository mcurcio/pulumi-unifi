#!/usr/bin/env bash
# Rebuild the synthetic class-file fixtures (testdata/classes) from their
# sources (testdata/java/fixture). The stubs in testdata/java/stubs are
# self-written annotation declarations with the same names as Spring, Jackson,
# Jakarta Validation, Hibernate Validator, Immutables and Swagger; they are
# compiled to a temp dir and only the fixture classes are kept. No Ubiquiti
# bytes are involved. Needs a JDK (javac). Run from anywhere.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
export JAVA_TOOL_OPTIONS= _JAVA_OPTIONS=
find "$HERE/java/stubs" -name '*.java' > "$TMP/stubs.txt"
javac -nowarn --release 17 -d "$TMP/stubs" @"$TMP/stubs.txt"
find "$HERE/java/fixture" -name '*.java' | sort > "$TMP/fix.txt"
rm -rf "$HERE/classes"
javac -nowarn --release 17 -g:none -cp "$TMP/stubs" -d "$HERE/classes" @"$TMP/fix.txt"
find "$HERE/classes" -name '*.class' | sort | sed "s#^$HERE/##"
