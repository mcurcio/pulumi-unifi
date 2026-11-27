// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.util.List;
import com.fasterxml.jackson.annotation.JsonProperty;
import jakarta.validation.constraints.Size;
import org.hibernate.validator.constraints.Length;
public class Member {
  @JsonProperty("id") private String id;
  @JsonProperty("name") @Length(max = 100) private String name;
  @JsonProperty("members") @Size(min = 1) private List<String> members;
  private String pskValue;
  public String getPskValue() { return pskValue; }
}
