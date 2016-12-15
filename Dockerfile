FROM scratch

ENTRYPOINT ["/configmapcontroller"]

COPY ./build/configmapcontroller-linux-amd64 /configmapcontroller
