// Self-written stub with the same name as Immutables', for compiling fixtures only.
// Default has CLASS retention, as upstream, so it lands in RuntimeInvisibleAnnotations.
package org.immutables.value;
import java.lang.annotation.*;
public @interface Value {
  @Retention(RetentionPolicy.CLASS) @Target(ElementType.METHOD) @interface Default {}
}
