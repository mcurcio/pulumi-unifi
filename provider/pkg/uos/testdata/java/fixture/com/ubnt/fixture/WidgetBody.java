// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.NotEmpty;
import jakarta.validation.constraints.NotNull;
import jakarta.validation.constraints.Pattern;
import jakarta.validation.constraints.Size;
import org.immutables.value.Value;
public interface WidgetBody {
  @JsonProperty("name") @NotEmpty @Size(min = 1, max = 64) String name();
  @JsonProperty("enabled") @Value.Default default boolean enabled() { return true; }
  @JsonProperty("mode") @Value.Default default Mode mode() { return Mode.FAST; }
  @JsonProperty("mode2") @Value.Default default Mode mode2() { return Defaults.DEFAULT_MODE; }
  @JsonProperty("count") @Min(0) @Max(100) @Value.Default default Integer count() { return 5; }
  @JsonProperty("big") @Value.Default default int big() { return 1000; }
  @JsonProperty("huge") @Value.Default default long huge() { return 100000L; }
  @JsonProperty("label") @Value.Default default String label() { return "abc"; }
  @JsonProperty("ratio") @Value.Default default double ratio() { return 0.5; }
  @JsonProperty("computed") @Value.Default default String computed() { return name() + "-x"; }
  @JsonProperty("tags") @Value.Default default List<@ValidMac String> tags() { return List.of(); }
  @JsonProperty("note") Optional<String> note();
  @JsonProperty("color") Color color();
  @JsonProperty("plain") Plain plain();
  @JsonProperty("schedule") @NotNull Schedule schedule();
  @JsonProperty("target") Matcher target();
  @JsonProperty("x_secret") String secret();
  @JsonProperty(value = "pass", access = JsonProperty.Access.WRITE_ONLY) String pass();
  @JsonProperty("limits") Map<String, Integer> limits();
  @JsonProperty("ref") UUID ref();
  @JsonProperty("when") Instant when();
  @JsonProperty("code") @Pattern(regexp = "^[a-z]+$") @NotAConstraint String code();
  @JsonProperty("matrix") int[][] matrix();
  @JsonIgnore @JsonProperty("hidden") String hidden();
  @JsonProperty("rq") @Schema(requiredMode = Schema.RequiredMode.REQUIRED) String rq();
  @JsonProperty("sw") @Schema(required = true) String sw();
}
