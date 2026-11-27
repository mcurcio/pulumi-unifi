// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.util.HashMap;
import java.util.Map;
import com.fasterxml.jackson.annotation.JsonValue;
public enum Plain {
  ALPHA("ignored"), BETA("ignored") { @Override public String toString() { return "b"; } };
  static final Map<String, Plain> LOOKUP = new HashMap<>();
  private final String extra;
  Plain(String extra) { this.extra = extra; }
  @JsonValue public String wire() { return name(); }
}
