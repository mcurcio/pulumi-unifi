// Self-written stub with the same name as Spring's, for compiling fixtures only.
package org.springframework.web.bind.annotation;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target(ElementType.PARAMETER)
public @interface RequestBody { String value() default ""; }
