ARG CI_SERVER_HOST
ARG CI_JOB_TOKEN
ARG CI_PROJECT_NAMESPACE

FROM golang:1.20.5-alpine3.17 AS builder

ARG CI_SERVER_HOST
ARG CI_JOB_TOKEN
ARG CI_PROJECT_NAMESPACE

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build

FROM alpine:3.18.3
COPY --from=builder /src/backend-scraper /backend-scraper
ENTRYPOINT [ "/backend-scraper" ]
