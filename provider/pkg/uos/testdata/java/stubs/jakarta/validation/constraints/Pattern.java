// Self-written stub with the same name as Jakarta Validation's, for compiling fixtures only.
package jakarta.validation.constraints;
import java.lang.annotation.*;
@Retention(RetentionPolicy.RUNTIME) @Target({ElementType.METHOD, ElementType.FIELD, ElementType.TYPE_USE})
public @interface Pattern { String regexp(); }
