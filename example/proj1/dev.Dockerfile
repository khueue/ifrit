FROM alpine:3.19

RUN apk add --no-cache bash curl

CMD ["/bin/bash"]
