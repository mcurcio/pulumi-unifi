// Self-written stub with the same name as Jackson's, for compiling fixtures only.
package com.fasterxml.jackson.annotation;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target({ElementType.FIELD, ElementType.METHOD, ElementType.PARAMETER})
public @interface JsonProperty {
  String value() default "";
  Access access() default Access.AUTO;
  enum Access { AUTO, READ_ONLY, WRITE_ONLY, READ_WRITE }
}
