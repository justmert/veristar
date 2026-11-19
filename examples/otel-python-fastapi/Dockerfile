# runs the fastapi example on the pinned python image, dependencies from uv.lock.
#   docker build -t otel-python-fastapi examples/otel-python-fastapi
# base images pinned by tag and index digest.

FROM ghcr.io/astral-sh/uv:0.13.9@sha256:d3b601f0c7b118bc1753febf7c41cc80981df678445e0254284eecf84db79a7d AS uv

FROM python:3.15.2-slim-trixie@sha256:65a65fc7b30b06cb6df4930fd4650697cb23f4c2698ab81fc87e4b031bc36928

# the stable http semantic conventions are the default; set this to an empty
# string at run time to get the legacy http.server.duration metric instead
# docs:start semconv
ENV OTEL_SEMCONV_STABILITY_OPT_IN=http
# docs:end semconv
ENV UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    UV_PYTHON_DOWNLOADS=never \
    PYTHONUNBUFFERED=1
WORKDIR /app
COPY pyproject.toml uv.lock ./
# --frozen installs exactly the lockfile; uv itself stays out of the final image
RUN --mount=from=uv,source=/uv,target=/bin/uv \
    uv sync --frozen --no-dev --no-cache --python /usr/local/bin/python3
COPY app.py ./
USER nobody
EXPOSE 8080
# uvicorn as pid 1 so sigterm triggers its graceful shutdown and the lifespan flush
CMD ["/app/.venv/bin/uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8080", "--no-access-log"]
