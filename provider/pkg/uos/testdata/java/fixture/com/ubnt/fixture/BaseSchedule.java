// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import com.fasterxml.jackson.annotation.JsonProperty;
public abstract class BaseSchedule {
  @JsonProperty("base_field") protected String baseField;
}
