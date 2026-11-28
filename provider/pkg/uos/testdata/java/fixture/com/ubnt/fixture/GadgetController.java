// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.util.List;
import org.springframework.web.bind.annotation.*;
@SiteApi
public class GadgetController {
  @GetMapping("/gadgets") public List<Gadget> list() { return null; }
  @PostMapping("/gadgets") public List<Gadget> create(@RequestBody List<Gadget> gadgets) { return null; }
  @PutMapping("/gadgets/{id}") public Gadget update(@PathVariable("id") String id, @RequestBody Gadget g) { return null; }
  @DeleteMapping(path = "/gadgets/{id}") public void delete(@PathVariable("id") String id) {}
}
