FROM golang:alpine AS builder
ARG VERSION
ARG GIT_COMMIT
ARG MODEL
ARG API_KEY
ARG API_URL
WORKDIR /app
ENV GOPROXY="https://goproxy.io"
COPY go.mod go.sum /app/
RUN go mod download
COPY . /app
RUN CGO_ENABLED=0  go build -ldflags "-s -w  -X main.Version=$VERSION -X main.GitCommit=$GIT_COMMIT  -X main.GitTag=$GIT_TAG  -X main.GitRepo=$GIT_REPOSITORY  -X main.BuildDate=$BUILD_DATE -X main.InnerModel=$MODEL -X main.InnerApiKey=$API_KEY -X main.InnerApiUrl=$API_URL" -o /app/k8m

FROM alpine:latest

WORKDIR /app
COPY --from=builder /app/k8m /app/k8m
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk upgrade && apk add --no-cache curl bash inotify-tools alpine-conf busybox-extras tzdata aws-cli  tar gzip\
    && apk del alpine-conf && rm -rf /var/cache/* && chmod +x k8m
ADD reload.sh /app/reload.sh
RUN chmod +x /app/reload.sh

RUN export VERIFY_CHECKSUM=false&&curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
RUN helm version


#k8m Server
EXPOSE 3618
ENTRYPOINT ["/app/reload.sh","k8m","/app"]