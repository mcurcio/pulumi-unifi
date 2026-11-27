// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.time.Instant;
import com.fasterxml.jackson.annotation.JsonProperty;
public interface WidgetView {
  @JsonProperty(value = "_id", access = JsonProperty.Access.READ_ONLY) String id();
  @JsonProperty("name") String name();
  @JsonProperty("created") Instant created();
  @JsonProperty("schedule") ScheduleView schedule();
}
