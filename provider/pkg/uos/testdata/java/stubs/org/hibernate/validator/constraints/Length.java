// Self-written stub with the same name as Hibernate Validator's, for compiling fixtures only.
package org.hibernate.validator.constraints;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target({ElementType.METHOD, ElementType.FIELD, ElementType.TYPE_USE})
public @interface Length { int min() default 0; int max() default Integer.MAX_VALUE; }
