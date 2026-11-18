# the stellar anchor platform image with the opentelemetry java agent added.
#   docker build -t anchor-platform-otel examples/otel-anchor-platform
# base image pinned by tag and index digest, the agent jar by version and sha256.

FROM stellar/anchor-platform:5.0.0@sha256:1189ace862c15da32236ad6d9f15c66117b42feb7c9fd08b1870555f6c39c821
# docs:start agent
WORKDIR /otel
ADD --chmod=444 --checksum=sha256:e43a270df53c6f423e0ab157be9c27ccc01d5dc3232e162fe54777573db2f149 \
    https://repo1.maven.org/maven2/io/opentelemetry/javaagent/opentelemetry-javaagent/2.33.1/opentelemetry-javaagent-2.33.1.jar \
    /otel/opentelemetry-javaagent.jar
WORKDIR /app
# the image's own entrypoint (/app/start.sh) runs java as a child of bash,
# which does not pass sigterm on, so the agent never flushes on a stop. java
# as the entrypoint receives it directly. JAVA_TOOL_OPTIONS takes the jvm
# options start.sh read from JVM_FLAGS.
ENTRYPOINT ["java", "-javaagent:/otel/opentelemetry-javaagent.jar", "-jar", "/app/anchor-platform-runner.jar"]
# docs:end agent
