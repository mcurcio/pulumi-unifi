// Self-written stub with the same name as Jackson's, for compiling fixtures only.
package com.fasterxml.jackson.annotation;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target(ElementType.TYPE)
public @interface JsonSubTypes {
  Type[] value();
  @interface Type { Class<?> value(); String name() default ""; }
}
