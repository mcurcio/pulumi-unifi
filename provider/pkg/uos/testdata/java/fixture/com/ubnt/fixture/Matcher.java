// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import com.fasterxml.jackson.annotation.JsonSubTypes;
import com.fasterxml.jackson.annotation.JsonTypeInfo;
@JsonTypeInfo(use = JsonTypeInfo.Id.NAME, property = "matching_target")
@JsonSubTypes({
  @JsonSubTypes.Type(value = AnyMatcher.class, name = "ANY"),
  @JsonSubTypes.Type(value = NetMatcher.class, name = "NETWORK")
})
public interface Matcher {}
