package example;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import sun.misc.Signal;

// a small accounts and payments api. the opentelemetry java agent, attached
// with -javaagent (start.sh), instruments it from the outside: this code
// knows nothing about exporters, endpoints or tokens. see README.md.
@SpringBootApplication
public class Application {
  public static void main(String[] args) {
    // docs:start signal
    // the jvm exits with 143 on sigterm. exiting with 0 instead runs the
    // same shutdown hooks (spring's graceful shutdown and the agent's flush of
    // both exporters) and tells the container runtime the stop was clean
    Signal.handle(new Signal("TERM"), signal -> System.exit(0));
    // docs:end signal
    SpringApplication.run(Application.class, args);
  }
}
