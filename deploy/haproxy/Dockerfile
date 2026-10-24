FROM haproxy:3.2.10-alpine@sha256:0ca7a59706bf4a0a2414165f874945be9e2c48cee0763665356a70c0f3848856
USER root
RUN mkdir -p /var/run/haproxy && chown haproxy:haproxy /var/run/haproxy
COPY haproxy.cfg /usr/local/etc/haproxy/haproxy.cfg
USER haproxy
