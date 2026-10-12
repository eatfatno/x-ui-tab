FROM alpine:3.22
RUN apk add --no-cache python3 ca-certificates tzdata bash curl openssl \
    && mkdir -p /app /data
WORKDIR /app
COPY panel/x-ui /app/x-ui
COPY panel/bin /app/seed/bin
COPY default-xray.json /app/default-xray.json
COPY traffic-monitor /app/traffic-monitor
COPY www /app/www
COPY container.py manifest.json /app/
COPY licenses /app/licenses
RUN chmod 755 /app/x-ui /app/traffic-monitor /app/seed/bin/xray-linux-amd64
ENV PYTHONDONTWRITEBYTECODE=1 TZ=Asia/Shanghai
LABEL org.opencontainers.image.title="3x-ui with traffic monitor" \
      org.opencontainers.image.description="Official 3x-ui, independent traffic monitor and Docker management CLI"
STOPSIGNAL SIGTERM
ENTRYPOINT ["python3", "/app/container.py"]
CMD ["serve"]
