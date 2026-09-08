FROM gcr.io/distroless/static-debian12

COPY incidentio-mock /usr/bin/incidentio-mock
COPY holmes-bridge /usr/bin/holmes-bridge
COPY fixtures /fixtures

ENTRYPOINT [ "/usr/bin/holmes-bridge" ]

CMD ["serve"]
