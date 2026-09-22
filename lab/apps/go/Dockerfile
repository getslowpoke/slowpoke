FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod go mod tidy && CGO_ENABLED=0 go build -o /goshop .

FROM alpine:3.20
COPY --from=build /goshop /goshop
CMD ["/goshop"]
