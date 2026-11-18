# runs the fastapi example on the pinned python image, dependencies from uv.lock.
#   docker build -t otel-python-fastapi examples/otel-python-fastapi
# base images pinned by tag and index digest.

FROM ghcr.io/astral-sh/uv:0.13.8@sha256:5cc1805e2426484abca4ad46ea783bab281fdb225fd82d8ce6d66a81254bac85 AS uv

FROM python:3.14.10-slim-trixie@sha256:9aaa474097d094040649f2162a627c0049b408f52138d980916788d80f04802f

# the stable http semantic conventions are the default; set this to an empty
# string at run time to get the legacy http.server.duration metric instead
# docs:start semconv (shown on the console's api connect page)
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
