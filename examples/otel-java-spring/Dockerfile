# builds the spring boot example with maven and runs it with the opentelemetry
# java agent attached.
#   docker build -t otel-java-spring examples/otel-java-spring
# base images pinned by tag and index digest. maven 3.10.1 exists only on
# maven central so far; 3.10.0 is the newest maven image.

FROM maven:3-eclipse-temurin-27-resolute@sha256:3175c44b207d6698aecb9bbb5bf285b5b4efcf7f567d6f65d88cd218276a6ab2 AS build
WORKDIR /src
COPY pom.xml ./
COPY src ./src
RUN --mount=type=cache,target=/root/.m2 mvn --batch-mode --no-transfer-progress package

FROM eclipse-temurin:25.0.5_8-jre-resolute@sha256:480ef64e4540d45b0301e8afb7873bd3fa61ce93c5f66273eb569420718f38e2
# created up front: a directory ADD creates would take the jar's mode
WORKDIR /app
# docs:start agent
# the agent jar from maven central, pinned by version and its sha256
ADD --chmod=444 --checksum=sha256:e43a270df53c6f423e0ab157be9c27ccc01d5dc3232e162fe54777573db2f149 \
    https://repo1.maven.org/maven2/io/opentelemetry/javaagent/opentelemetry-javaagent/2.33.1/opentelemetry-javaagent-2.33.1.jar \
    /app/opentelemetry-javaagent.jar
# docs:end agent
COPY --from=build /src/target/app.jar /app/app.jar
COPY --chmod=444 otel.yaml otel-traces.yaml /app/
COPY --chmod=555 start.sh /app/
USER nobody
EXPOSE 8080
# the image's entrypoint (/__cacert_entrypoint.sh) runs this; USE_SYSTEM_CA_CERTS=1
# with certificates mounted under /certificates adds a private ca for the jvm
CMD ["/app/start.sh"]
