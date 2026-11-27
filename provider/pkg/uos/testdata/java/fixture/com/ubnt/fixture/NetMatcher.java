// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.util.List;
import com.fasterxml.jackson.annotation.JsonProperty;
public interface NetMatcher extends Matcher {
  @JsonProperty("network_ids") List<String> networkIds();
}
