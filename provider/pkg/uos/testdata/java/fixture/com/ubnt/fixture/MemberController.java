// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import org.springframework.web.bind.annotation.*;
@SiteApi
public class MemberController {
  @PostMapping("/members") public Member create(@RequestBody Member m) { return m; }
  @GetMapping("/members/{id}") public Member get(@PathVariable("id") String id) { return null; }
  @PutMapping("/members/{id}") public Member update(@PathVariable("id") String id, @RequestBody Member m) { return m; }
}
