// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.lang.annotation.*;
import org.springframework.web.bind.annotation.RequestMapping;
@Retention(RetentionPolicy.RUNTIME) @Target(ElementType.TYPE)
@RequestMapping("/api/site/{siteName}")
public @interface SiteApi {}
