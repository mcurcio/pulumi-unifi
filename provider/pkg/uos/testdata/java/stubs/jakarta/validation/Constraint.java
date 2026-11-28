// Self-written stub with the same name as Jakarta Validation's, for compiling fixtures only.
package jakarta.validation;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target(ElementType.ANNOTATION_TYPE)
public @interface Constraint { Class<?>[] validatedBy(); }
