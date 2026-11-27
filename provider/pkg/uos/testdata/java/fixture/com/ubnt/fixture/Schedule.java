// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonProperty;
import jakarta.validation.constraints.NotNull;
public class Schedule extends BaseSchedule {
  private static int counter;
  @JsonProperty("mode") @NotNull private Mode mode;
  private boolean allDay;
  private String noGetter;
  @JsonIgnore private String ignored;
  public Mode getMode() { return mode; }
  public boolean isAllDay() { return allDay; }
  public String getIgnored() { return ignored; }
}
