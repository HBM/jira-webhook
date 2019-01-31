
# start from golang base image
FROM golang:1.11.4

# make modules work
ENV GO111MODULE=on

# set working directory
WORKDIR /go/src/github.com/zemirco/jira-webhook

# copy everything into docker
ADD . .

# build binary
RUN go build -o main .

# export port 8060 to the outside world
EXPOSE 8060

# run the binary
ENTRYPOINT ["./main"]