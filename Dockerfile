FROM scratch

ENTRYPOINT ["/configmapcontroller"]

COPY bin/kubectl /kubectl
COPY ./build/configmapcontroller-linux-amd64 /configmapcontroller
