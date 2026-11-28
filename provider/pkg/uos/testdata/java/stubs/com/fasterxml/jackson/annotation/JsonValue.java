// Self-written stub with the same name as Jackson's, for compiling fixtures only.
package com.fasterxml.jackson.annotation;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target({ElementType.FIELD, ElementType.METHOD})
public @interface JsonValue { boolean value() default true; }
