// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import com.fasterxml.jackson.annotation.JsonValue;
public enum Mode {
  FAST("fast"), SLOW("slow");
  private final String wire;
  Mode(String wire) { this.wire = wire; }
  @JsonValue public String wire() { return wire; }
}
