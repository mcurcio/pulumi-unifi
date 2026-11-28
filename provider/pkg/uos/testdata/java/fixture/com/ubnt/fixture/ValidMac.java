// Synthetic test fixture written for this repository (not Ubiquiti code): it
// carries the same annotations the extractor reads, under a com.ubnt package
// name because the extractor models only com.ubnt classes.
package com.ubnt.fixture;
import java.lang.annotation.*;
import jakarta.validation.Constraint;
@Constraint(validatedBy = {}) @Retention(RetentionPolicy.RUNTIME)
@Target({ElementType.TYPE_USE, ElementType.METHOD, ElementType.FIELD})
public @interface ValidMac { String message() default "must be a valid MAC"; }
