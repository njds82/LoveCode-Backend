.PHONY: build test vet run
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/lovecode ./cmd/lovecode
test:
	go test -race -count=1 ./...
vet:
	go vet ./...
run: build
	./bin/lovecode -config configs/lovecode.example.toml
