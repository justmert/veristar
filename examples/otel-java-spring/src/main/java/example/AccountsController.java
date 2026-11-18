package example;

import io.opentelemetry.api.GlobalOpenTelemetry;
import io.opentelemetry.api.common.AttributeKey;
import io.opentelemetry.api.common.Attributes;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.SpanKind;
import io.opentelemetry.api.trace.Tracer;
import io.opentelemetry.context.Scope;
import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class AccountsController {
  // a positive decimal with up to 7 fractional digits, the stellar amount precision
  private static final Pattern AMOUNT = Pattern.compile("^\\d+(\\.\\d{1,7})?$");
  private static final AttributeKey<String> LOOKUP = AttributeKey.stringKey("account.lookup");

  // the agent registers its sdk as the global instance before main runs
  private final Tracer tracer = GlobalOpenTelemetry.getTracer("otel-java-spring");
  private final Map<String, Map<String, String>> accounts = new HashMap<>();
  private final Map<String, List<Map<String, String>>> payments = new HashMap<>();
  private long next;

  private static ResponseEntity<Object> error(HttpStatus status, String message) {
    return ResponseEntity.status(status).body(Map.of("error", message));
  }

  // an internal span with an event, both carrying marker strings, so the
  // platform tests can prove non-server spans and events never leave its
  // collector while the tenant backend still receives them
  private Map<String, String> lookup(String id) {
    Span span = tracer.spanBuilder("SA_MARKER_internal_span").setSpanKind(SpanKind.INTERNAL).startSpan();
    try (Scope ignored = span.makeCurrent()) {
      span.addEvent("SA_MARKER_span_event", Attributes.of(LOOKUP, id));
      synchronized (this) {
        return accounts.get(id);
      }
    } finally {
      span.end();
    }
  }

  @GetMapping("/healthz")
  public ResponseEntity<Void> healthz() {
    return ResponseEntity.noContent().build();
  }

  @PostMapping("/accounts")
  public ResponseEntity<Object> createAccount(@RequestBody(required = false) Map<String, Object> body) {
    Object name = body == null ? null : body.get("name");
    if (!(name instanceof String s) || s.isBlank()) {
      return error(HttpStatus.BAD_REQUEST, "name is required");
    }
    Map<String, String> account;
    synchronized (this) {
      account = Map.of("id", Long.toString(++next), "name", s.strip());
      accounts.put(account.get("id"), account);
      payments.put(account.get("id"), new ArrayList<>());
    }
    return ResponseEntity.status(HttpStatus.CREATED).body(account);
  }

  @GetMapping("/accounts/{id}")
  public ResponseEntity<Object> getAccount(@PathVariable String id) {
    Map<String, String> account = lookup(id);
    if (account == null) {
      return error(HttpStatus.NOT_FOUND, "account not found");
    }
    return ResponseEntity.ok(account);
  }

  @PostMapping("/accounts/{id}/payments")
  public ResponseEntity<Object> createPayment(
      @PathVariable String id, @RequestBody(required = false) Map<String, Object> body) {
    if (lookup(id) == null) {
      return error(HttpStatus.NOT_FOUND, "account not found");
    }
    Object amount = body == null ? null : body.get("amount");
    if (!(amount instanceof String a) || !AMOUNT.matcher(a).matches() || new BigDecimal(a).signum() <= 0) {
      return error(HttpStatus.BAD_REQUEST, "amount must be a positive decimal");
    }
    Map<String, String> payment;
    synchronized (this) {
      List<Map<String, String>> list = payments.get(id);
      payment = Map.of("id", Integer.toString(list.size() + 1), "amount", a);
      list.add(payment);
    }
    return ResponseEntity.status(HttpStatus.CREATED).body(payment);
  }

  @GetMapping("/accounts/{id}/payments")
  public ResponseEntity<Object> listPayments(@PathVariable String id) {
    if (lookup(id) == null) {
      return error(HttpStatus.NOT_FOUND, "account not found");
    }
    synchronized (this) {
      return ResponseEntity.ok(Map.of("payments", List.copyOf(payments.get(id))));
    }
  }
}
