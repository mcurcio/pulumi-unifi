// Self-written stub with the same name as Spring's, for compiling fixtures only.
package org.springframework.web.bind.annotation;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target({ElementType.TYPE, ElementType.METHOD, ElementType.ANNOTATION_TYPE})
public @interface RequestMapping { String[] value() default {}; String[] path() default {}; RequestMethod[] method() default {}; }
