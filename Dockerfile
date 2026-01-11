FROM golang:1.25.4 AS build  
WORKDIR /app  
COPY . .  
RUN CGO_ENABLED=0 GOOS=linux go build -o app -ldflags="-s -w"  

FROM scratch  
COPY --from=build /app/app /  
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
CMD ["/app"]  
