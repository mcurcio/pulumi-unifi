// Self-written stub with the same name as Swagger's, for compiling fixtures only.
package io.swagger.v3.oas.annotations.media;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target({ElementType.FIELD, ElementType.METHOD, ElementType.TYPE})
public @interface Schema {
  boolean required() default false;
  String example() default "";
  AccessMode accessMode() default AccessMode.AUTO;
  RequiredMode requiredMode() default RequiredMode.AUTO;
  enum AccessMode { AUTO, READ_ONLY, WRITE_ONLY, READ_WRITE }
  enum RequiredMode { AUTO, REQUIRED, NOT_REQUIRED }
}
