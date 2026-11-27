// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.io.IOException;
import java.util.List;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
@RequestMapping("/api/site/{siteName}/widgets")
public class WidgetController {
  @GetMapping public List<WidgetView> list() { return null; }
  @PostMapping public ResponseEntity<WidgetView> create(@RequestBody WidgetBody body) { return null; }
  @GetMapping("/{id}") public WidgetView get(@PathVariable("id") String id) throws IOException { return null; }
  @PutMapping("/{id}") public WidgetView update(@PathVariable("id") String id, @RequestBody WidgetBody body) { return null; }
  @DeleteMapping({"/{id}"}) public void delete(@PathVariable("id") String id) {}
  @RequestMapping(value = "/{id}/ping", method = {RequestMethod.POST, RequestMethod.PUT}) public void ping() {}
}
