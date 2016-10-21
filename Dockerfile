FROM scratch

ENTRYPOINT ["/configmapcontroller"]

COPY ./out/configmapcontroller-linux-amd64 /configmapcontroller
