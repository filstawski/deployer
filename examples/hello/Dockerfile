FROM golang:1.27-alpine AS build
WORKDIR /src
COPY main.go .
ARG REVISION=development
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.Revision=${REVISION}" -o /hello main.go

FROM scratch
COPY --from=build /hello /hello
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/hello"]
