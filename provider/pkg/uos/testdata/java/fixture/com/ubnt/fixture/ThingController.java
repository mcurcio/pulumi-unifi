// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.util.List;
import org.springframework.web.bind.annotation.*;
@SiteApi
public class ThingController {
  @GetMapping("/things") public List<Gadget> list() { return null; }
  @PostMapping("/things/create") public Gadget create(@RequestBody Gadget g) { return g; }
  @DeleteMapping("/things/{thingId}") public void delete(@PathVariable("thingId") String id) {}
  @GetMapping("/things/{thingId}/status") public String status() { return null; }
}
